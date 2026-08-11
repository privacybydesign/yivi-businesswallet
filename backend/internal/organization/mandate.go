package organization

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// The two mandate tiers of Recital 18. A full mandate lets the grantee act on the
// owner's behalf generally; an administrative mandate lets them assign roles and
// responsibilities to users within its scope. Full strictly contains
// administrative, which is what bounds a delegation.
const (
	MandateFull           = "full"
	MandateAdministrative = "administrative"
)

// Where a mandate reaches. Only an org-wide mandate carries org-wide
// administrative authority; a department-scoped one is confined to its department.
const (
	MandateScopeOrganization = "organization"
	MandateScopeDepartment   = "department"
)

// The lifecycle a mandate moves through (grant -> active -> revoked/expired).
// Derived from revoked_at and the validity window against the clock, never stored
// — an expiry has to take effect at request time, and a stored status is only as
// fresh as whatever last wrote it (Annex §12(3)(b)).
const (
	MandateStatusPending = "pending"
	MandateStatusActive  = "active"
	MandateStatusRevoked = "revoked"
	MandateStatusExpired = "expired"
)

var (
	ErrMandateNotFound = errors.New("mandate not found")
	ErrMandateInactive = errors.New("mandate is not active")
	// ErrNotMandateAuthority means the caller holds no basis of authority that may
	// grant or revoke a mandate — they are neither a legal representative nor an
	// active full-mandate holder.
	ErrNotMandateAuthority = errors.New("caller may not grant or revoke mandates")
	// ErrOverDelegation means a delegated grant asked for more than the mandate it
	// was cut from holds: a higher tier, a scope the parent does not cover, or a
	// window that survives the parent.
	ErrOverDelegation = errors.New("delegated mandate exceeds the mandate it is cut from")
	// ErrGranteeNotMember means the grantee has no membership in the organization.
	// A mandate is authority to act for this owner; it needs someone to act as.
	ErrGranteeNotMember = errors.New("mandate grantee is not a member of the organization")
	// ErrMandateWindow means the requested validity window is empty (valid_until at
	// or before valid_from).
	ErrMandateWindow = errors.New("mandate validity window is empty")
	// ErrMandateType means the requested tier is neither full nor administrative.
	ErrMandateType = errors.New("unknown mandate type")
	// ErrMandateScope means the requested scope and its department do not agree: a
	// department-scoped mandate needs a department, an org-wide one may not carry
	// one.
	ErrMandateScope = errors.New("mandate scope and department do not agree")
	// ErrMandateEffectiveInPast means an effective-dated revocation was asked to
	// take effect at a moment that has already passed. Revoking with immediate
	// effect is a separate, explicit request.
	ErrMandateEffectiveInPast = errors.New("revocation effective date is in the past")
)

// Mandate is one grant of authority inside the wallet: the owner (through a legal
// representative) authorising a member to act on its behalf, or a mandate holder
// delegating part of what it holds. See .ai/features/mandates.md.
type Mandate struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organizationId"`
	Type           string    `json:"type"`
	// Status is derived at read time, not stored.
	Status              string     `json:"status"`
	GrantorUserID       *uuid.UUID `json:"grantorUserId"`
	GrantorName         *string    `json:"grantorName"`
	GranteeUserID       uuid.UUID  `json:"granteeUserId"`
	GranteeName         string     `json:"granteeName"`
	Scope               string     `json:"scope"`
	ScopeDepartmentID   *uuid.UUID `json:"scopeDepartmentId"`
	ScopeDepartmentName *string    `json:"scopeDepartmentName"`
	ParentMandateID     *uuid.UUID `json:"parentMandateId"`
	ValidFrom           time.Time  `json:"validFrom"`
	ValidUntil          *time.Time `json:"validUntil"`
	RevokedAt           *time.Time `json:"revokedAt"`
	RevokedByUserID     *uuid.UUID `json:"revokedByUserId"`
	RevocationReason    *string    `json:"revocationReason"`
	CreatedAt           time.Time  `json:"createdAt"`
}

// MandateGrant is a requested grant, before it is checked against the mandate it
// is delegated from. ParentMandateID is nil for a root grant, which only a legal
// representative may make.
type MandateGrant struct {
	Type              string
	GranteeUserID     uuid.UUID
	Scope             string
	ScopeDepartmentID *uuid.UUID
	ParentMandateID   *uuid.UUID
	ValidFrom         time.Time
	ValidUntil        *time.Time
}

// Authority is the caller's basis of authority in the resolved organization —
// Axis A of .ai/plans/rbac-model.md. Authorize resolves it from the database on
// every org-scoped request, so a revoked or expired mandate stops working on the
// next request rather than whenever a sweep gets to it.
type Authority struct {
	// LegalRepresentative reports a claimed, unrevoked, in-window `bestuurder` row
	// in wallet_representations: the register-backed root of authority, and the
	// only basis that may grant a mandate nobody delegated to it.
	LegalRepresentative bool
	// FullMandate reports an active org-wide mandate of type full.
	FullMandate bool
	// Mandated reports at least one active org-wide mandate, of either type.
	Mandated bool
	// Granted counts every mandate ever granted to the caller in this
	// organization, whatever its state — what tells "never had one" apart from
	// "had one and lost it".
	Granted int
	// PlatformAdmin marks the deployment-level platform admin. It is orthogonal to
	// the org's own authority (rbac-model.md: "Platform admin stays
	// deployment-level and orthogonal"), so the owner's mandate register never
	// withdraws it — an org cannot lock the operator out of its own deployment.
	PlatformAdmin bool
}

// Withdrawn reports that the caller's mandated authority has been taken away:
// they have been granted mandates in this organization, and none of them is now
// an active org-wide one — every grant revoked, expired, not yet in force, or
// narrowed to a single department. An organization that has never granted a
// mandate is never withdrawn, so the mandate layer stays opt-in per org.
func (a Authority) Withdrawn() bool { return !a.PlatformAdmin && a.Granted > 0 && !a.Mandated }

// MayGrantMandate reports whether the caller may grant or revoke a mandate. Gated
// on Axis A alone: no functional role reaches it, so an admin cannot mint itself
// a mandate (rbac-model.md, "Assignment & delegation").
func (a Authority) MayGrantMandate() bool { return a.LegalRepresentative || a.FullMandate }

// mandateRank orders the tiers so a delegation can be compared with what it is
// cut from. Unknown types rank 0 and so can never be delegated.
func mandateRank(mandateType string) int {
	switch mandateType {
	case MandateFull:
		return 2
	case MandateAdministrative:
		return 1
	default:
		return 0
	}
}

// mandateStatus derives the lifecycle state of a mandate at now. Revocation wins
// over expiry: a mandate revoked before its window closed was revoked, and the
// audit trail should say so.
func mandateStatus(m Mandate, now time.Time) string {
	switch {
	case m.RevokedAt != nil && !m.RevokedAt.After(now):
		return MandateStatusRevoked
	case m.ValidUntil != nil && !m.ValidUntil.After(now):
		return MandateStatusExpired
	case m.ValidFrom.After(now):
		return MandateStatusPending
	default:
		return MandateStatusActive
	}
}

// clampToParent narrows a delegated grant to what its parent actually holds
// (Annex §12(3)(b): no over-delegation). Tier and scope must already fit — asking
// for more is an error, not something to silently rewrite, because a caller who
// asked for a full mandate should not be told they got one. The window is clamped
// instead, per rbac-model.md: a delegation may not outlive the authority it was
// cut from, and trimming its end is not a different grant.
func clampToParent(req MandateGrant, parent Mandate, now time.Time) (MandateGrant, error) {
	if mandateStatus(parent, now) != MandateStatusActive {
		return MandateGrant{}, ErrMandateInactive
	}
	if mandateRank(req.Type) == 0 || mandateRank(req.Type) > mandateRank(parent.Type) {
		return MandateGrant{}, ErrOverDelegation
	}
	if parent.Scope == MandateScopeDepartment {
		if req.Scope != MandateScopeDepartment || req.ScopeDepartmentID == nil ||
			parent.ScopeDepartmentID == nil || *req.ScopeDepartmentID != *parent.ScopeDepartmentID {
			return MandateGrant{}, ErrOverDelegation
		}
	}

	if req.ValidFrom.Before(parent.ValidFrom) {
		req.ValidFrom = parent.ValidFrom
	}
	if parent.ValidUntil != nil && (req.ValidUntil == nil || req.ValidUntil.After(*parent.ValidUntil)) {
		until := *parent.ValidUntil
		req.ValidUntil = &until
	}
	if req.ValidUntil != nil && !req.ValidUntil.After(req.ValidFrom) {
		// The parent runs out before the delegation would start, so there is no
		// authority left to cut from.
		return MandateGrant{}, ErrOverDelegation
	}
	return req, nil
}
