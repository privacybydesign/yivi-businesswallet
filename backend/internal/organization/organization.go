package organization

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	RoleAdmin  = "admin"
	RoleMember = "member"

	StatusActive  = "active"
	StatusInvited = "invited"

	DefaultMemberListLimit = 25
	MaxMemberListLimit     = 100
)

var (
	ErrNotFound            = errors.New("organization not found")
	ErrSlugTaken           = errors.New("organization slug already taken")
	ErrNotMember           = errors.New("user is not a member of the organization")
	ErrAlreadyMember       = errors.New("user is already a member of the organization")
	ErrAlreadyInvited      = errors.New("user is already invited to the organization")
	ErrInvitationNotFound  = errors.New("invitation not found")
	ErrInvitationExpired   = errors.New("invitation expired")
	ErrEmailMismatch       = errors.New("disclosed email does not match the invitation")
	ErrNameMismatch        = errors.New("disclosed name does not match the invitation")
	ErrDisclosureFailed    = errors.New("identity disclosure failed")
	ErrReviewNotFound      = errors.New("identity review not found")
	ErrReviewResolved      = errors.New("identity review already resolved")
	ErrLastAdmin           = errors.New("cannot demote the last admin of the organization")
	ErrDepartmentNotFound  = errors.New("department not found")
	ErrDepartmentNameTaken = errors.New("department name already taken")
	ErrDepartmentInUse     = errors.New("department still has members")
)

type Organization struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Slug string    `json:"slug"`
}

type Department struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organizationId"`
	Name           string    `json:"name"`
}

type Membership struct {
	UserID         uuid.UUID  `json:"userId"`
	OrganizationID uuid.UUID  `json:"organizationId"`
	Role           string     `json:"role"`
	JobTitle       *string    `json:"jobTitle"`
	DepartmentID   *uuid.UUID `json:"departmentId"`
}

type Invitation struct {
	ID               uuid.UUID  `json:"id"`
	OrganizationID   uuid.UUID  `json:"organizationId"`
	OrganizationName string     `json:"organizationName,omitempty"`
	OrganizationSlug string     `json:"organizationSlug,omitempty"`
	Token            string     `json:"-"`
	Email            string     `json:"email"`
	InvitedBy        *uuid.UUID `json:"invitedBy"`
	Role             string     `json:"role"`
	JobTitle         *string    `json:"jobTitle"`
	DepartmentID     *uuid.UUID `json:"departmentId"`
	DepartmentName   *string    `json:"departmentName"`
	GivenNames       string     `json:"givenNames"`
	LastName         string     `json:"lastName"`
	ExpiresAt        time.Time  `json:"expiresAt"`
	CreatedAt        time.Time  `json:"createdAt"`
}

type Member struct {
	UserID         uuid.UUID  `json:"userId"`
	Email          string     `json:"email"`
	PreferredName  *string    `json:"preferredName"`
	GivenNames     string     `json:"givenNames"`
	LastName       string     `json:"lastName"`
	Role           string     `json:"role"`
	JobTitle       *string    `json:"jobTitle"`
	DepartmentID   *uuid.UUID `json:"departmentId"`
	DepartmentName *string    `json:"departmentName"`
}

type MemberEntry struct {
	Status         string     `json:"status"`
	UserID         *uuid.UUID `json:"userId"`
	InvitationID   *uuid.UUID `json:"invitationId"`
	Email          string     `json:"email"`
	PreferredName  *string    `json:"preferredName"`
	GivenNames     string     `json:"givenNames"`
	LastName       string     `json:"lastName"`
	Role           string     `json:"role"`
	JobTitle       *string    `json:"jobTitle"`
	DepartmentID   *uuid.UUID `json:"departmentId"`
	DepartmentName *string    `json:"departmentName"`
	ExpiresAt      *time.Time `json:"expiresAt"`
	InvitedBy      *uuid.UUID `json:"invitedBy"`
}

type MemberListParams struct {
	Status string
	Search string
	Sort   string
	Desc   bool
	Limit  int
	Offset int
}
