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
	Create(ctx context.Context, u user.User) (user.User, error)
}

type membershipStore interface {
	AddMembership(ctx context.Context, orgID, userID uuid.UUID, role string, jobTitle *string, departmentID *uuid.UUID) (Member, error)
}

type Service struct {
	users       userStore
	memberships membershipStore
}

func NewService(users userStore, memberships membershipStore) *Service {
	return &Service{users: users, memberships: memberships}
}

type Invite struct {
	Email         user.Email
	PreferredName *string
	GivenNames    string
	NamePrefix    *string
	LastName      string
	Role          string
	JobTitle      *string
	DepartmentID  *uuid.UUID
}

func (s *Service) InviteMember(ctx context.Context, orgID uuid.UUID, in Invite) (Member, error) {
	u, err := s.findOrCreateUser(ctx, in)
	if err != nil {
		return Member{}, err
	}
	return s.memberships.AddMembership(ctx, orgID, u.ID, in.Role, in.JobTitle, in.DepartmentID)
}

func (s *Service) findOrCreateUser(ctx context.Context, in Invite) (user.User, error) {
	u, err := s.users.FindByEmail(ctx, in.Email)
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, user.ErrNotFound) {
		return user.User{}, fmt.Errorf("invite: find user: %w", err)
	}

	created, err := s.users.Create(ctx, user.User{
		Email:         in.Email,
		PreferredName: in.PreferredName,
		GivenNames:    in.GivenNames,
		NamePrefix:    in.NamePrefix,
		LastName:      in.LastName,
	})
	if errors.Is(err, user.ErrEmailTaken) {
		// Lost a race with a concurrent invite of the same new email; reuse it.
		return s.users.FindByEmail(ctx, in.Email)
	}
	if err != nil {
		return user.User{}, fmt.Errorf("invite: create user: %w", err)
	}
	return created, nil
}
