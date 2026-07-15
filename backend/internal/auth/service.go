package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/session"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

type PendingInvite struct {
	ID               uuid.UUID
	OrganizationName string
	OrganizationSlug string
}

// invitationLookup lets login surface pending invitations for an email that has
// no account yet, so a brand-new invitee is routed to accept instead of a
// dead-end. Implemented by the organization store, injected to avoid a cycle.
type invitationLookup interface {
	PendingInvitationsForEmail(ctx context.Context, email user.Email) ([]PendingInvite, error)
}

// PendingInvitesError signals a successful email disclosure for someone with no
// account but open invitations: not an error to the caller, a routing signal.
type PendingInvitesError struct {
	Invites []PendingInvite
}

func (e *PendingInvitesError) Error() string {
	return "auth: no account for this email, but pending invitations exist"
}

// Session is a started OpenID4VP presentation returned to the client: an opaque
// id to poll plus the wallet deeplink to render as a QR / universal link.
type Session struct {
	ID         string `json:"id"`
	WalletLink string `json:"walletLink"`
}

type DisclosedIdentity struct {
	Email user.Email
	Name  identity.Name
}

// verifier is the slice of openid4vpverifier.Client the service needs, defined in
// the consumer so the service is testable without a live verifier.
type verifier interface {
	StartPresentation(ctx context.Context) (openid4vpverifier.Session, error)
	Result(ctx context.Context, id string) (openid4vpverifier.Presentation, error)
	Status(ctx context.Context, id string) (string, error)
}

type Service struct {
	verifier verifier
	users    *user.Store
	sessions *session.Store
	invites  invitationLookup
}

func NewService(
	verifier verifier,
	users *user.Store,
	sessions *session.Store,
	invites invitationLookup,
) *Service {
	return &Service{
		verifier: verifier,
		users:    users,
		sessions: sessions,
		invites:  invites,
	}
}

func (s *Service) StartSession(ctx context.Context) (Session, error) {
	sess, err := s.verifier.StartPresentation(ctx)
	if err != nil {
		return Session{}, fmt.Errorf("auth: start session: %w", err)
	}
	return Session{ID: sess.ID, WalletLink: sess.WalletLink}, nil
}

// StartIdentitySession begins a disclosure for the invite-accept flow. With
// OpenID4VP a login already discloses identity (passport/id-card) plus email, so
// login and identity-accept use the same presentation request.
func (s *Service) StartIdentitySession(ctx context.Context) (Session, error) {
	return s.StartSession(ctx)
}

func (s *Service) DiscloseIdentity(ctx context.Context, id string) (DisclosedIdentity, error) {
	res, err := s.result(ctx, id)
	if err != nil {
		return DisclosedIdentity{}, err
	}
	return extractIdentity(res)
}

func (s *Service) Status(ctx context.Context, id string) (string, error) {
	return s.verifier.Status(ctx, id)
}

func (s *Service) Authenticate(ctx context.Context, id string) (user.User, string, error) {
	res, err := s.result(ctx, id)
	if err != nil {
		return user.User{}, "", err
	}

	email, err := extractEmail(res)
	if err != nil {
		return user.User{}, "", err
	}

	u, err := s.users.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return user.User{}, "", s.invitedOrNotFound(ctx, email)
		}
		return user.User{}, "", err
	}

	// idempotencyKey = sha256(sessionID) makes a replayed /claim rotate this
	// user's session row rather than mint a second one.
	raw, err := s.sessions.Mint(ctx, u.ID, sha256.Sum256([]byte(id)))
	if err != nil {
		return user.User{}, "", err
	}
	return u, raw, nil
}

// result fetches the presentation, translating a not-yet-complete presentation
// (ErrPending) into errSessionNotFinished so the handler maps it to 409.
func (s *Service) result(ctx context.Context, id string) (openid4vpverifier.Presentation, error) {
	res, err := s.verifier.Result(ctx, id)
	if errors.Is(err, openid4vpverifier.ErrPending) {
		return openid4vpverifier.Presentation{}, errSessionNotFinished
	}
	if err != nil {
		return openid4vpverifier.Presentation{}, err
	}
	return res, nil
}

func (s *Service) invitedOrNotFound(ctx context.Context, email user.Email) error {
	if s.invites == nil {
		return errUserNotInvited
	}
	invites, err := s.invites.PendingInvitationsForEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("auth: lookup pending invitations: %w", err)
	}
	if len(invites) == 0 {
		return errUserNotInvited
	}
	return &PendingInvitesError{Invites: invites}
}

func (s *Service) Logout(ctx context.Context, rawToken string) error {
	return s.sessions.Delete(ctx, rawToken)
}
