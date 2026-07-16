package organization

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

type userStore interface {
	FindByEmail(ctx context.Context, email user.Email) (user.User, error)
	Create(ctx context.Context, u user.User) (user.User, error)
	UpdateName(ctx context.Context, id uuid.UUID, givenNames, lastName string) error
}

type invitationStore interface {
	GetMembership(ctx context.Context, userID, orgID uuid.UUID) (Membership, error)
	CreateInvitation(ctx context.Context, in Invitation) (Invitation, error)
	InvitationByToken(ctx context.Context, rawToken string) (Invitation, error)
	InvitationByID(ctx context.Context, invitationID uuid.UUID) (Invitation, error)
	ListInvitationsForEmail(ctx context.Context, email string) ([]Invitation, error)
	AcceptInvitation(ctx context.Context, inv Invitation, userID uuid.UUID, disclosed identity.Name, phone string) error
	RecordRejectedAccept(ctx context.Context, orgID uuid.UUID, email string, before, after map[string]any) error
	DeclineInvitation(ctx context.Context, rawToken string) error
	DeclineInvitationByID(ctx context.Context, invitationID uuid.UUID) error
	CreateIdentityReview(ctx context.Context, inv Invitation, userID uuid.UUID, stored, disclosed identity.Name, phone string) (ReviewState, error)
	ListIdentityReviews(ctx context.Context) ([]IdentityReview, error)
	ResolveIdentityReview(ctx context.Context, reviewID, reviewerID uuid.UUID, approve bool) (ResolveOutcome, error)
}

type identityDiscloser interface {
	StartIdentitySession(ctx context.Context) (auth.Session, error)
	DiscloseIdentity(ctx context.Context, sessionID string) (auth.DisclosedIdentity, error)
}

type Service struct {
	users     userStore
	store     invitationStore
	discloser identityDiscloser
}

func NewService(users userStore, store invitationStore, discloser identityDiscloser) *Service {
	return &Service{users: users, store: store, discloser: discloser}
}

type Invite struct {
	Email        user.Email
	GivenNames   string
	LastName     string
	Role         string
	JobTitle     *string
	DepartmentID *uuid.UUID
	InvitedBy    uuid.UUID
}

func (s *Service) InviteMember(ctx context.Context, orgID uuid.UUID, in Invite) (Invitation, error) {
	switch u, err := s.users.FindByEmail(ctx, in.Email); {
	case err == nil:
		switch _, mErr := s.store.GetMembership(ctx, u.ID, orgID); {
		case mErr == nil:
			return Invitation{}, ErrAlreadyMember
		case !errors.Is(mErr, ErrNotMember):
			return Invitation{}, fmt.Errorf("invite: check membership: %w", mErr)
		}
	case !errors.Is(err, user.ErrNotFound):
		return Invitation{}, fmt.Errorf("invite: find user: %w", err)
	}

	invitedBy := in.InvitedBy
	return s.store.CreateInvitation(ctx, Invitation{
		OrganizationID: orgID,
		Email:          string(in.Email),
		InvitedBy:      &invitedBy,
		Role:           in.Role,
		JobTitle:       in.JobTitle,
		DepartmentID:   in.DepartmentID,
		GivenNames:     in.GivenNames,
		LastName:       in.LastName,
	})
}

func (s *Service) PendingInvitation(ctx context.Context, rawToken string) (Invitation, error) {
	inv, err := s.store.InvitationByToken(ctx, rawToken)
	if err != nil {
		return Invitation{}, err
	}
	if time.Now().After(inv.ExpiresAt) {
		return Invitation{}, ErrInvitationExpired
	}
	return inv, nil
}

func (s *Service) StartAcceptSession(ctx context.Context, rawToken string) (auth.Session, error) {
	if _, err := s.PendingInvitation(ctx, rawToken); err != nil {
		return auth.Session{}, err
	}
	return s.discloser.StartIdentitySession(ctx)
}

// StartIdentitySession begins an identity disclosure not bound to a specific
// invitation, for the by-id accept flows (in-app and login-routed). The
// invitation is selected at accept time and the disclosure's email-match gates it.
func (s *Service) StartIdentitySession(ctx context.Context) (auth.Session, error) {
	return s.discloser.StartIdentitySession(ctx)
}

type AcceptStatus string

const (
	AcceptAccepted      AcceptStatus = "accepted"
	AcceptPendingReview AcceptStatus = "pending_review"
)

type AcceptOutcome struct {
	Status           AcceptStatus
	UserID           uuid.UUID
	OrganizationSlug string
	OrganizationName string
}

func (s *Service) AcceptInvitation(ctx context.Context, rawToken, disclosureToken string) (AcceptOutcome, error) {
	inv, err := s.PendingInvitation(ctx, rawToken)
	if err != nil {
		return AcceptOutcome{}, err
	}
	return s.acceptResolved(ctx, inv, disclosureToken)
}

func (s *Service) acceptResolved(ctx context.Context, inv Invitation, disclosureToken string) (AcceptOutcome, error) {
	disclosed, err := s.discloser.DiscloseIdentity(ctx, disclosureToken)
	if err != nil {
		return AcceptOutcome{}, ErrDisclosureFailed
	}

	if !strings.EqualFold(string(disclosed.Email), inv.Email) {
		_ = s.store.RecordRejectedAccept(ctx, inv.OrganizationID, inv.Email,
			map[string]any{"email": inv.Email},
			map[string]any{"email": string(disclosed.Email)})
		return AcceptOutcome{}, ErrEmailMismatch
	}
	invited := identity.Name{GivenNames: inv.GivenNames, LastName: inv.LastName}
	if disclosed.Name.Key() != invited.Key() {
		_ = s.store.RecordRejectedAccept(ctx, inv.OrganizationID, inv.Email,
			map[string]any{"givenNames": inv.GivenNames, "lastName": inv.LastName},
			map[string]any{"givenNames": disclosed.Name.GivenNames, "lastName": disclosed.Name.LastName})
		return AcceptOutcome{}, ErrNameMismatch
	}

	u, needsReview, err := s.resolveUser(ctx, disclosed.Email, disclosed.Name)
	if err != nil {
		return AcceptOutcome{}, err
	}
	at := AcceptOutcome{UserID: u.ID, OrganizationSlug: inv.OrganizationSlug, OrganizationName: inv.OrganizationName}

	if needsReview {
		stored := identity.Name{GivenNames: u.GivenNames, LastName: u.LastName}
		state, err := s.store.CreateIdentityReview(ctx, inv, u.ID, stored, disclosed.Name, disclosed.Phone)
		if err != nil {
			return AcceptOutcome{}, err
		}
		if state == ReviewRejected {
			return AcceptOutcome{}, ErrIdentityRejected
		}
		at.Status = AcceptPendingReview
		return at, nil
	}

	if err := s.store.AcceptInvitation(ctx, inv, u.ID, disclosed.Name, disclosed.Phone); err != nil {
		return AcceptOutcome{}, err
	}
	at.Status = AcceptAccepted
	return at, nil
}

func (s *Service) resolveUser(ctx context.Context, email user.Email, disclosed identity.Name) (user.User, bool, error) {
	u, err := s.users.FindByEmail(ctx, email)
	if errors.Is(err, user.ErrNotFound) {
		cleaned := disclosed.Clean()
		minted, err := s.users.Create(ctx, user.User{Email: email, GivenNames: cleaned.GivenNames, LastName: cleaned.LastName})
		return minted, false, err
	}
	if err != nil {
		return user.User{}, false, fmt.Errorf("accept: find user: %w", err)
	}

	stored := identity.Name{GivenNames: u.GivenNames, LastName: u.LastName}
	switch identity.Reconcile(disclosed, &stored) {
	case identity.Review:
		return u, true, nil
	case identity.Upgrade:
		cleaned := disclosed.Clean()
		if err := s.users.UpdateName(ctx, u.ID, cleaned.GivenNames, cleaned.LastName); err != nil {
			return user.User{}, false, fmt.Errorf("accept: upgrade name: %w", err)
		}
	}
	return u, false, nil
}

func (s *Service) DeclineInvitation(ctx context.Context, rawToken string) error {
	return s.store.DeclineInvitation(ctx, rawToken)
}

func (s *Service) MyInvitations(ctx context.Context, email user.Email) ([]Invitation, error) {
	return s.store.ListInvitationsForEmail(ctx, string(email))
}

func (s *Service) AcceptInvitationByID(ctx context.Context, invitationID uuid.UUID, disclosureToken string) (AcceptOutcome, error) {
	inv, err := s.store.InvitationByID(ctx, invitationID)
	if err != nil {
		return AcceptOutcome{}, err
	}
	if time.Now().After(inv.ExpiresAt) {
		return AcceptOutcome{}, ErrInvitationExpired
	}
	return s.acceptResolved(ctx, inv, disclosureToken)
}

func (s *Service) DeclineInvitationForUser(ctx context.Context, invitationID uuid.UUID, email user.Email) error {
	if _, err := s.myInvitation(ctx, invitationID, email); err != nil {
		return err
	}
	return s.store.DeclineInvitationByID(ctx, invitationID)
}

// myInvitation loads an invitation by id and confirms it was addressed to the
// caller, returning ErrInvitationNotFound otherwise so one user cannot probe or
// act on another's invitation.
func (s *Service) myInvitation(ctx context.Context, invitationID uuid.UUID, email user.Email) (Invitation, error) {
	inv, err := s.store.InvitationByID(ctx, invitationID)
	if err != nil {
		return Invitation{}, err
	}
	if !strings.EqualFold(inv.Email, string(email)) {
		return Invitation{}, ErrInvitationNotFound
	}
	return inv, nil
}

func (s *Service) ListIdentityReviews(ctx context.Context) ([]IdentityReview, error) {
	return s.store.ListIdentityReviews(ctx)
}

func (s *Service) ResolveIdentityReview(ctx context.Context, reviewID, reviewerID uuid.UUID, approve bool) (ResolveOutcome, error) {
	return s.store.ResolveIdentityReview(ctx, reviewID, reviewerID, approve)
}
