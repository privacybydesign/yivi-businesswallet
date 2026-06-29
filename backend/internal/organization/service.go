package organization

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

type userStore interface {
	FindByEmail(ctx context.Context, email user.Email) (user.User, error)
}

type invitationStore interface {
	GetMembership(ctx context.Context, userID, orgID uuid.UUID) (Membership, error)
	CreateInvitation(ctx context.Context, in Invitation) (Invitation, error)
}

type Service struct {
	users userStore
	store invitationStore
}

func NewService(users userStore, store invitationStore) *Service {
	return &Service{users: users, store: store}
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
