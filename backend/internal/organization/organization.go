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

	// Organization (business wallet) lifecycle statuses (Art 6(2)). StatusActive
	// is shared with the member-status value above.
	StatusSuspended = "suspended"
	StatusRevoked   = "revoked"

	// The owner's standing instruction for their data when the provider stops
	// serving them (Art 7(6)(f)). InstructionTransfer hands the bundle over;
	// InstructionDelete hands it over and then owes erasure.
	InstructionTransfer = "transfer"
	InstructionDelete   = "delete"

	DefaultMemberListLimit = 25
	MaxMemberListLimit     = 100

	// Member type (#240): chosen at invite time, editable by an admin. Governs
	// which re-identification interval (IdentitySettings) applies.
	MemberTypeEmployee = "employee"
	MemberTypeExternal = "external"

	// Re-identification status, derived from IdentityVerifiedAt / IdentityDueAt /
	// IdentityRequestedAt — never stored independently (see DeriveIdentityStatus).
	IdentityStatusNever     = "never"
	IdentityStatusVerified  = "verified"
	IdentityStatusDueSoon   = "due_soon"
	IdentityStatusOverdue   = "overdue"
	IdentityStatusRequested = "requested"

	// Overdue consequence (IdentitySettings.OverdueConsequence): flag-only (the
	// default) surfaces the status but changes nothing else; block additionally
	// refuses credential issuance and signing as a signer (see IdentityBlocked).
	OverdueConsequenceFlag  = "flag"
	OverdueConsequenceBlock = "block"

	DefaultReidentifyTokenTTL = 30 * 24 * time.Hour
)

var (
	ErrIdentitySettingsInvalid = errors.New("invalid identity settings")
	ErrReverifyTokenNotFound   = errors.New("re-identification link not found or expired")
	ErrReverifyEmailMismatch   = errors.New("disclosed email does not match this member")
	ErrReverifyNameMismatch    = errors.New("disclosed name does not match this member's identity on file")
	ErrCredentialTooOld        = errors.New("disclosed credential is older than the organization's freshness policy allows")
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
	ErrIdentityRejected    = errors.New("identity was rejected for this invitation")
	ErrReviewNotFound      = errors.New("identity review not found")
	ErrReviewResolved      = errors.New("identity review already resolved")
	ErrLastAdmin           = errors.New("cannot demote the last admin of the organization")
	ErrDepartmentNotFound  = errors.New("department not found")
	ErrDepartmentNameTaken = errors.New("department name already taken")
	ErrDepartmentInUse     = errors.New("department still has members")
)

// Organization is a business wallet: identity from the KVK register plus the
// wallet's QERDS digital address and lifecycle status.
type Organization struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"` // the register's official legal name
	Slug           string    `json:"slug"`
	KVKNumber      string    `json:"kvkNumber"`
	EUID           string    `json:"euid"`
	DigitalAddress string    `json:"digitalAddress"`
	Status         string    `json:"status"`
	BootstrappedAt time.Time `json:"bootstrappedAt"`
	// DataInstruction is what to do with the owner's data on termination.
	DataInstruction string `json:"dataInstruction"`
	// TerminatedAt is when the provider ended service, nil while it has not.
	TerminatedAt *time.Time `json:"terminatedAt,omitempty"`
	// ErasurePendingAt is set when a termination honoured a delete instruction:
	// the bundle went out and erasure is owed. Destruction stays a deliberate
	// operator step, so this marks the debt rather than settling it.
	ErasurePendingAt *time.Time `json:"erasurePendingAt,omitempty"`
	// LogoURI is the API path serving the org's theme logo, or "" when none is
	// set. Only the list endpoints (List/ListForUser) populate it, so the org
	// switcher can show each org's logo without a per-org theme fetch; the
	// single-org endpoints leave it empty.
	LogoURI string `json:"logoUri,omitempty"`
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
	ID                   uuid.UUID  `json:"id"`
	OrganizationID       uuid.UUID  `json:"organizationId"`
	OrganizationName     string     `json:"organizationName,omitempty"`
	OrganizationSlug     string     `json:"organizationSlug,omitempty"`
	Token                string     `json:"-"`
	Email                string     `json:"email"`
	InvitedBy            *uuid.UUID `json:"invitedBy"`
	Role                 string     `json:"role"`
	JobTitle             *string    `json:"jobTitle"`
	DepartmentID         *uuid.UUID `json:"departmentId"`
	DepartmentName       *string    `json:"departmentName"`
	GivenNames           string     `json:"givenNames"`
	LastName             string     `json:"lastName"`
	MemberType           string     `json:"memberType"`
	ExternalOrganisation *string    `json:"externalOrganisation"`
	ExpiresAt            time.Time  `json:"expiresAt"`
	CreatedAt            time.Time  `json:"createdAt"`
	ReviewStatus         string     `json:"-"`
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
	Phone          *string    `json:"phone"`
	// Verified reports that the member proved a passport/id-card identity when they
	// joined; orthogonal to the active/invited status. Derived from
	// IdentityVerifiedAt, never set independently of it.
	Verified bool `json:"verified"`
	// IdentityVerifiedAt is when the member last proved a passport/id-card
	// identity (accept, an admin-approved identity review, or a completed
	// re-identification); nil means never.
	IdentityVerifiedAt *time.Time `json:"identityVerifiedAt"`
	// IdentityDueAt is when the current identification lapses under the org's
	// re-identification policy; nil when no policy applies (off for this member's
	// type, or never verified).
	IdentityDueAt *time.Time `json:"identityDueAt"`
	// IdentityRequestedAt / IdentityRequestedBy record an admin's on-demand
	// "request identification"; nil when none is outstanding.
	IdentityRequestedAt *time.Time `json:"identityRequestedAt"`
	IdentityRequestedBy *uuid.UUID `json:"identityRequestedBy"`
	// IdentityStatus is derived from the three fields above (see
	// DeriveIdentityStatus): "never" · "verified" · "due_soon" · "overdue" ·
	// "requested". Set by the handler, which knows the org's reminder window.
	IdentityStatus string `json:"identityStatus"`
	// MemberType is "employee" or "external" (MemberTypeEmployee /
	// MemberTypeExternal), chosen at invite time and editable by an admin.
	MemberType string `json:"memberType"`
	// ExternalOrganisation is optional free text naming who an external works
	// for; meaningful only when MemberType is external.
	ExternalOrganisation *string `json:"externalOrganisation"`
	// AvatarURI is the API path serving this member's portrait photo, "" when they
	// have not set one. Set by the handler from HasAvatar / AvatarUpdatedAt, which
	// are the store's answer and never reach the client on their own.
	AvatarURI       string     `json:"avatarUri"`
	HasAvatar       bool       `json:"-"`
	AvatarUpdatedAt *time.Time `json:"-"`
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
	Phone          *string    `json:"phone"`
	// Verified is always false for invited entries (no identity proven yet).
	Verified bool `json:"verified"`
	// IdentityVerifiedAt is always nil for invited entries (no membership row yet).
	IdentityVerifiedAt *time.Time `json:"identityVerifiedAt"`
	// IdentityDueAt / IdentityRequestedAt are always nil for invited entries; see
	// the Member fields of the same name.
	IdentityDueAt       *time.Time `json:"identityDueAt"`
	IdentityRequestedAt *time.Time `json:"identityRequestedAt"`
	// IdentityStatus is "never" for every invited entry (set by the handler,
	// mirroring Member.IdentityStatus).
	IdentityStatus string `json:"identityStatus"`
	// MemberType / ExternalOrganisation carry the invitation's intended values for
	// a pending entry, applied to the membership on accept.
	MemberType           string  `json:"memberType"`
	ExternalOrganisation *string `json:"externalOrganisation"`
	// AvatarURI is the API path serving this member's portrait photo, "" when they
	// have not set one — always "" for an invited entry, which has no user row yet.
	AvatarURI       string     `json:"avatarUri"`
	HasAvatar       bool       `json:"-"`
	AvatarUpdatedAt *time.Time `json:"-"`
}

type MemberListParams struct {
	Status string
	Search string
	Sort   string
	Desc   bool
	Limit  int
	Offset int
}

// IdentitySettings is an org's re-identification policy (#240 §3). Configured is
// false when the org has never saved any, in which case the feature is off: no
// due dates are computed and no reminders are sent.
type IdentitySettings struct {
	Configured bool `json:"configured"`
	// EmployeeIntervalMonths / ExternalIntervalMonths: nil = off (that member type
	// never becomes due).
	EmployeeIntervalMonths *int `json:"employeeIntervalMonths"`
	ExternalIntervalMonths *int `json:"externalIntervalMonths"`
	// ReminderDaysBefore lists how many days before IdentityDueAt to remind a
	// member, e.g. [30, 14, 7]. The largest value is also the "due soon" lookahead
	// window (see DeriveIdentityStatus). int32 (not int) because it round-trips a
	// Postgres int4[] column through pgx's native array codec.
	ReminderDaysBefore []int32 `json:"reminderDaysBefore"`
	// OverdueReminderIntervalDays / OverdueReminderMaxCount bound the repeat
	// reminders sent once a member is overdue.
	OverdueReminderIntervalDays int `json:"overdueReminderIntervalDays"`
	OverdueReminderMaxCount     int `json:"overdueReminderMaxCount"`
	// CredentialMaxAgeDays: nil = off. When set, re-identification rejects a
	// disclosed credential whose issuer `iat` is older than this.
	CredentialMaxAgeDays *int `json:"credentialMaxAgeDays"`
	// OverdueConsequence is OverdueConsequenceFlag (default) or
	// OverdueConsequenceBlock.
	OverdueConsequence string     `json:"overdueConsequence"`
	UpdatedAt          *time.Time `json:"updatedAt,omitempty"`
}

// IdentitySettingsInput is a full replacement of an org's identity settings.
type IdentitySettingsInput struct {
	EmployeeIntervalMonths      *int
	ExternalIntervalMonths      *int
	ReminderDaysBefore          []int32
	OverdueReminderIntervalDays int
	OverdueReminderMaxCount     int
	CredentialMaxAgeDays        *int
	OverdueConsequence          string
}

// reminderLookaheadDays is the "due soon" window DeriveIdentityStatus uses when
// an org has not configured a reminder schedule (Configured false, or an empty
// ReminderDaysBefore) — the same default the schema column ships.
const reminderLookaheadDays = 30

// LookaheadDays is the "due soon" window: the largest configured reminder
// threshold, or the default when none is configured.
func (s IdentitySettings) LookaheadDays() int {
	max := int32(0)
	for _, d := range s.ReminderDaysBefore {
		if d > max {
			max = d
		}
	}
	if max == 0 {
		return reminderLookaheadDays
	}
	return int(max)
}

// IntervalFor returns the configured re-identification interval for a member
// type, nil when off.
func (s IdentitySettings) IntervalFor(memberType string) *int {
	if memberType == MemberTypeExternal {
		return s.ExternalIntervalMonths
	}
	return s.EmployeeIntervalMonths
}
