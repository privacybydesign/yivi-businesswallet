package organization

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	irma "github.com/privacybydesign/irmago/irma"
	irmaserver "github.com/privacybydesign/irmago/irma/server"

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
	AcceptInvitation(ctx context.Context, inv Invitation, userID uuid.UUID, disclosed identity.Name) error
	DeclineInvitation(ctx context.Context, rawToken string) error
}

type identityDiscloser interface {
	StartIdentitySession(ctx context.Context) (*irmaserver.SessionPackage, error)
	DiscloseIdentity(ctx context.Context, token irma.RequestorToken) (auth.DisclosedIdentity, error)
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

func (s *Service) StartAcceptSession(ctx context.Context, rawToken string) (*irmaserver.SessionPackage, error) {
	if _, err := s.PendingInvitation(ctx, rawToken); err != nil {
		return nil, err
	}
	return s.discloser.StartIdentitySession(ctx)
}

type AcceptOutcome struct {
	OrganizationSlug string
	OrganizationName string
}

func (s *Service) AcceptInvitation(ctx context.Context, rawToken, disclosureToken string) (AcceptOutcome, error) {
	inv, err := s.PendingInvitation(ctx, rawToken)
	if err != nil {
		return AcceptOutcome{}, err
	}

	disclosed, err := s.discloser.DiscloseIdentity(ctx, irma.RequestorToken(disclosureToken))
	if err != nil {
		return AcceptOutcome{}, ErrDisclosureFailed
	}

	if !strings.EqualFold(string(disclosed.Email), inv.Email) {
		return AcceptOutcome{}, ErrEmailMismatch
	}
	invited := identity.Name{GivenNames: inv.GivenNames, LastName: inv.LastName}
	if disclosed.Name.Key() != invited.Key() {
		return AcceptOutcome{}, ErrNameMismatch
	}

	u, err := s.resolveUser(ctx, disclosed.Email, disclosed.Name)
	if err != nil {
		return AcceptOutcome{}, err
	}

	if err := s.store.AcceptInvitation(ctx, inv, u.ID, disclosed.Name); err != nil {
		return AcceptOutcome{}, err
	}
	return AcceptOutcome{OrganizationSlug: inv.OrganizationSlug, OrganizationName: inv.OrganizationName}, nil
}

func (s *Service) resolveUser(ctx context.Context, email user.Email, disclosed identity.Name) (user.User, error) {
	u, err := s.users.FindByEmail(ctx, email)
	if errors.Is(err, user.ErrNotFound) {
		cleaned := disclosed.Clean()
		return s.users.Create(ctx, user.User{Email: email, GivenNames: cleaned.GivenNames, LastName: cleaned.LastName})
	}
	if err != nil {
		return user.User{}, fmt.Errorf("accept: find user: %w", err)
	}

	stored := identity.Name{GivenNames: u.GivenNames, LastName: u.LastName}
	switch identity.Reconcile(disclosed, &stored) {
	case identity.Review:
		return user.User{}, ErrIdentityReview
	case identity.Upgrade:
		cleaned := disclosed.Clean()
		if err := s.users.UpdateName(ctx, u.ID, cleaned.GivenNames, cleaned.LastName); err != nil {
			return user.User{}, fmt.Errorf("accept: upgrade name: %w", err)
		}
	}
	return u, nil
}

func (s *Service) DeclineInvitation(ctx context.Context, rawToken string) error {
	return s.store.DeclineInvitation(ctx, rawToken)
}
