package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/google/uuid"
	irma "github.com/privacybydesign/irmago/irma"
	irmaserver "github.com/privacybydesign/irmago/irma/server"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
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

type IdentityAttributes struct {
	GivenNames irma.AttributeTypeIdentifier
	FamilyName irma.AttributeTypeIdentifier
}

type DisclosedIdentity struct {
	Email user.Email
	Name  identity.Name
}

// irmaRequestor is the slice of irmarequestor.Client the service needs, defined
// in the consumer so the service is testable without a live daemon.
type irmaRequestor interface {
	StartSession(ctx context.Context, req *irma.DisclosureRequest) (*irmaserver.SessionPackage, error)
	Result(ctx context.Context, token irma.RequestorToken) (*irmaserver.SessionResult, error)
	Status(ctx context.Context, token irma.RequestorToken) (irma.ServerStatus, error)
}

type Service struct {
	irma          irmaRequestor
	users         *user.Store
	sessions      *session.Store
	emailAttr     irma.AttributeTypeIdentifier
	identityAttrs IdentityAttributes
	invites       invitationLookup
}

func NewService(
	requestor irmaRequestor,
	users *user.Store,
	sessions *session.Store,
	emailAttr irma.AttributeTypeIdentifier,
	identityAttrs IdentityAttributes,
	invites invitationLookup,
) *Service {
	return &Service{
		irma:          requestor,
		users:         users,
		sessions:      sessions,
		emailAttr:     emailAttr,
		identityAttrs: identityAttrs,
		invites:       invites,
	}
}

func (s *Service) StartSession(ctx context.Context) (*irmaserver.SessionPackage, error) {
	req := irma.NewDisclosureRequest(s.emailAttr)

	pkg, err := s.irma.StartSession(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("auth: start session: %w", err)
	}
	return pkg, nil
}

func (s *Service) StartIdentitySession(ctx context.Context) (*irmaserver.SessionPackage, error) {
	req := irma.NewDisclosureRequest(s.identityAttrs.GivenNames, s.identityAttrs.FamilyName, s.emailAttr)

	pkg, err := s.irma.StartSession(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("auth: start identity session: %w", err)
	}
	return pkg, nil
}

func (s *Service) DiscloseIdentity(ctx context.Context, token irma.RequestorToken) (DisclosedIdentity, error) {
	res, err := s.irma.Result(ctx, token)
	if err != nil {
		return DisclosedIdentity{}, err
	}
	return extractIdentity(res, s.identityAttrs, s.emailAttr)
}

func (s *Service) Status(ctx context.Context, token irma.RequestorToken) (irma.ServerStatus, error) {
	return s.irma.Status(ctx, token)
}

func (s *Service) Authenticate(ctx context.Context, token irma.RequestorToken) (user.User, string, error) {
	res, err := s.irma.Result(ctx, token)
	if err != nil {
		return user.User{}, "", err
	}

	email, err := extractEmail(res, s.emailAttr)
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

	// idempotencyKey = sha256(requestorToken) makes a replayed /claim rotate this user's session row rather than mint a second one.
	raw, err := s.sessions.Mint(ctx, u.ID, sha256.Sum256([]byte(token)))
	if err != nil {
		return user.User{}, "", err
	}
	return u, raw, nil
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
