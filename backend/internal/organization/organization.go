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
	// DefaultVogTokenTTL matches the re-identification link: long enough for a
	// member to apply for a VOG at Justis and receive it before the link lapses.
	DefaultVogTokenTTL = 30 * 24 * time.Hour

	// Screening / VOG (#242): who an org requires a VOG for.
	ScreeningRequiredForNobody    = "nobody"
	ScreeningRequiredForEmployees = "employees"
	ScreeningRequiredForExternals = "externals"
	ScreeningRequiredForBoth      = "both"

	// RecheckAnchor is which date ScreeningSettings' recheck interval counts
	// from: the VOG's own issue date (default) or the date it was checked.
	RecheckAnchorIssueDate = "issue_date"
	RecheckAnchorCheckedAt = "checked_at"

	// Screening status, derived from ScreeningSettings plus the member's latest
	// screening attempt - never stored independently (see
	// DeriveScreeningStatus).
	ScreeningStatusNotRequired     = "not_required"
	ScreeningStatusNone            = "none"
	ScreeningStatusRequested       = "requested"
	ScreeningStatusValid           = "valid"
	ScreeningStatusExpiring        = "expiring"
	ScreeningStatusExpired         = "expired"
	ScreeningStatusRejected        = "rejected"
	ScreeningStatusRecheckRequired = "recheck_required"

	// Who ran a screening check (member_screenings.checked_by).
	CheckedBySelf  = "self"
	CheckedByAdmin = "admin"
)

var (
	ErrIdentitySettingsInvalid = errors.New("invalid identity settings")
	ErrReverifyTokenNotFound   = errors.New("re-identification link not found or expired")
	ErrReverifyEmailMismatch   = errors.New("disclosed email does not match this member")
	ErrReverifyNameMismatch    = errors.New("disclosed name does not match this member's identity on file")
	ErrCredentialTooOld        = errors.New("disclosed credential is older than the organization's freshness policy allows")
)

var (
	ErrScreeningSettingsInvalid = errors.New("invalid screening settings")
	ErrVogTokenNotFound         = errors.New("VOG link not found or expired")
	ErrVogNoDateOfBirth         = errors.New("member has no date of birth on file; re-identification is required first")
	ErrVogNotAVOG               = errors.New("uploaded document is not a recognisable VOG")
	ErrVogUnparseable           = errors.New("a required field on the VOG could not be read")
	ErrVogTooOld                = errors.New("the VOG's issue date is older than the organization allows")
	ErrVogCredentialNotAccepted = errors.New("this organization does not accept the pbdf.vog credential")
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
	// VogLastResult / VogValidUntil / VogCoveredCodes mirror the member's latest
	// screening attempt (nil/empty when never checked). VogStatus is derived from
	// them plus the org's requirement (see DeriveScreeningStatus); set by the
	// handler, which knows the org's screening settings.
	VogLastResult   *string    `json:"-"`
	VogValidUntil   *time.Time `json:"vogValidUntil"`
	VogCoveredCodes []string   `json:"-"`
	VogRequestedAt  *time.Time `json:"vogRequestedAt"`
	VogRequestedBy  *uuid.UUID `json:"vogRequestedBy"`
	VogStatus       string     `json:"vogStatus"`
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
	// VogLastResult / VogValidUntil / VogCoveredCodes / VogRequestedAt / VogStatus
	// are always nil/empty/"not_required" for an invited entry (no membership row
	// yet); see the Member fields of the same name.
	VogLastResult   *string    `json:"-"`
	VogValidUntil   *time.Time `json:"vogValidUntil"`
	VogCoveredCodes []string   `json:"-"`
	VogRequestedAt  *time.Time `json:"vogRequestedAt"`
	VogRequestedBy  *uuid.UUID `json:"vogRequestedBy"`
	VogStatus       string     `json:"vogStatus"`
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

// ScreeningSettings is an org's VOG policy (#242 §3). Configured is false when
// the org has never saved any, in which case RequiredFor is
// ScreeningRequiredForNobody and the feature is off: no member is ever asked
// for a VOG.
type ScreeningSettings struct {
	Configured bool `json:"configured"`
	// RequiredFor is one of ScreeningRequiredForNobody/Employees/Externals/Both.
	RequiredFor string `json:"requiredFor"`
	// RequiredCodes are the function-aspect (internal/vog.FunctionAspects) and/or
	// specific-profile codes a member's VOG must cover.
	RequiredCodes []string `json:"requiredCodes"`
	// MaxAgeAtUploadDays: nil = off. When set, a VOG whose issue date is older
	// than this at upload/disclosure is rejected.
	MaxAgeAtUploadDays *int `json:"maxAgeAtUploadDays"`
	// EmployeeRecheckIntervalMonths / ExternalRecheckIntervalMonths: nil = off for
	// that member type.
	EmployeeRecheckIntervalMonths *int `json:"employeeRecheckIntervalMonths"`
	ExternalRecheckIntervalMonths *int `json:"externalRecheckIntervalMonths"`
	// RecheckAnchor is RecheckAnchorIssueDate (default) or RecheckAnchorCheckedAt.
	RecheckAnchor string `json:"recheckAnchor"`
	// ReminderDaysBefore / OverdueReminderIntervalDays / OverdueReminderMaxCount /
	// OverdueConsequence mirror IdentitySettings' fields of the same name.
	ReminderDaysBefore          []int32 `json:"reminderDaysBefore"`
	OverdueReminderIntervalDays int     `json:"overdueReminderIntervalDays"`
	OverdueReminderMaxCount     int     `json:"overdueReminderMaxCount"`
	OverdueConsequence          string  `json:"overdueConsequence"`
	// AcceptYiviCredential opts in to the pbdf.vog credential disclosure path
	// alongside the always-on PDF upload.
	AcceptYiviCredential bool       `json:"acceptYiviCredential"`
	UpdatedAt            *time.Time `json:"updatedAt,omitempty"`
}

// ScreeningSettingsInput is a full replacement of an org's screening settings.
type ScreeningSettingsInput struct {
	RequiredFor                   string
	RequiredCodes                 []string
	MaxAgeAtUploadDays            *int
	EmployeeRecheckIntervalMonths *int
	ExternalRecheckIntervalMonths *int
	RecheckAnchor                 string
	ReminderDaysBefore            []int32
	OverdueReminderIntervalDays   int
	OverdueReminderMaxCount       int
	OverdueConsequence            string
	AcceptYiviCredential          bool
}

// screeningLookaheadDays is the "expiring soon" window DeriveScreeningStatus
// uses when an org has not configured a reminder schedule, mirroring
// reminderLookaheadDays.
const screeningLookaheadDays = 30

// LookaheadDays is the "expiring soon" window: the largest configured reminder
// threshold, or the default when none is configured.
func (s ScreeningSettings) LookaheadDays() int {
	max := int32(0)
	for _, d := range s.ReminderDaysBefore {
		if d > max {
			max = d
		}
	}
	if max == 0 {
		return screeningLookaheadDays
	}
	return int(max)
}

// RecheckIntervalFor returns the configured re-check interval for a member
// type, nil when off.
func (s ScreeningSettings) RecheckIntervalFor(memberType string) *int {
	if memberType == MemberTypeExternal {
		return s.ExternalRecheckIntervalMonths
	}
	return s.EmployeeRecheckIntervalMonths
}

// RequiredForMember reports whether the policy requires a VOG for a member of
// memberType.
func (s ScreeningSettings) RequiredForMember(memberType string) bool {
	switch s.RequiredFor {
	case ScreeningRequiredForBoth:
		return true
	case ScreeningRequiredForEmployees:
		return memberType != MemberTypeExternal
	case ScreeningRequiredForExternals:
		return memberType == MemberTypeExternal
	default:
		return false
	}
}

// ScreeningRecord is one row of a member's VOG screening history
// (member_screenings).
type ScreeningRecord struct {
	ID              uuid.UUID  `json:"id"`
	Method          string     `json:"method"`
	Result          string     `json:"result"`
	CheckedAt       time.Time  `json:"checkedAt"`
	VogIssueDate    *time.Time `json:"vogIssueDate"`
	ValidUntil      *time.Time `json:"validUntil"`
	CoveredCodes    []string   `json:"coveredCodes"`
	MissingCodes    []string   `json:"missingCodes"`
	CheckedBy       string     `json:"checkedBy"`
	CheckedByUserID *uuid.UUID `json:"checkedByUserId"`
}
