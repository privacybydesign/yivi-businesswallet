package organization

import (
	"errors"

	"github.com/google/uuid"
)

const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

var (
	ErrNotFound  = errors.New("organization not found")
	ErrSlugTaken = errors.New("organization slug already taken")
	ErrNotMember = errors.New("user is not a member of the organization")
)

type Organization struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Slug string    `json:"slug"`
}

type Membership struct {
	UserID         uuid.UUID `json:"userId"`
	OrganizationID uuid.UUID `json:"organizationId"`
	Role           string    `json:"role"`
}

type Member struct {
	UserID uuid.UUID `json:"userId"`
	Email  string    `json:"email"`
	Role   string    `json:"role"`
}
