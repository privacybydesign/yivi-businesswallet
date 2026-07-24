// Package consent implements the consent & approval layer designed in
// .ai/plans/consent-approval-layer.md (#113): the approval queue that holds
// pending presentation/issuance payloads, and the policy engine that can
// auto-decide them. It is one governance layer serving both directions — the
// presentation flow (#112) enqueues "what we disclose" and the holder-acceptance
// flow (#32) enqueues "what we accept"; both act only on a decided item plus its
// approved attribute subset.
//
// The layer is the model + mechanism: the queue store, the code-first policy
// matcher, and the audit integration. The HTTP surface and UI, and the wiring
// into the presentation/acceptance flows, land with #112/#32 against this model
// (per the design's phasing). The approvals/policies permissions it is gated on
// live in the RBAC matrix (internal/organization/permissions.go), already active.
//
// Scope and validity fields are carried from day one but v1 enforces org-wide
// scope with no validity window, mirroring the RBAC seam; #27 turns window and
// scope narrowing on. Revocation of a policy is immediate.
package consent

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Kind is the direction of an approval item.
type Kind string

const (
	KindPresentation Kind = "presentation" // disclose attributes to a verifier
	KindIssuance     Kind = "issuance"     // accept a held credential from an issuer
)

func (k Kind) valid() bool { return k == KindPresentation || k == KindIssuance }

// Status is the lifecycle state of an approval request.
type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusDeclined Status = "declined"
	StatusExpired  Status = "expired"
	// StatusSupersededByPolicy is reserved for the on-author re-evaluation
	// follow-up (a policy authored after an item is already pending); it is part
	// of the model vocabulary but no v1 path produces it, since policies are
	// evaluated only when an item is enqueued.
	StatusSupersededByPolicy Status = "superseded_by_policy"
)

// Mode is which of the four authorisation modes resolved an item.
type Mode string

const (
	// ModeHumanInitiated is reserved: an operator initiating a session is itself
	// consent, so such actions never enter the queue. Carried for completeness.
	ModeHumanInitiated Mode = "human_initiated"
	ModeHumanApproved  Mode = "human_approved" // queued for a human decider
	ModePolicyAuto     Mode = "policy_auto"    // auto-decided by a matching policy
	ModeFourEyes       Mode = "four_eyes"      // queued, needs two distinct approvers
)

// Effect is what a matching policy does.
type Effect string

const (
	EffectAutoApprove Effect = "auto_approve"
	EffectAutoDecline Effect = "auto_decline"
)

func (e Effect) valid() bool { return e == EffectAutoApprove || e == EffectAutoDecline }

// ApprovalRequest is one item in the queue.
type ApprovalRequest struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organizationId"`
	Kind           Kind       `json:"kind"`
	Counterparty   string     `json:"counterparty"`
	Requested      []string   `json:"requested"`
	Status         Status     `json:"status"`
	Mode           Mode       `json:"mode"`
	DecidedSubset  []string   `json:"decidedSubset"`
	DecidedBy      *uuid.UUID `json:"decidedBy"`
	DecidedAt      *time.Time `json:"decidedAt"`
	DualDecidedBy  *uuid.UUID `json:"dualDecidedBy"`
	DualDecidedAt  *time.Time `json:"dualDecidedAt"`
	PolicyID       *uuid.UUID `json:"policyId"`
	ExpiresAt      time.Time  `json:"expiresAt"`
	CreatedAt      time.Time  `json:"createdAt"`
}

// Policy is an admin-authored auto-decide rule.
type Policy struct {
	ID                  uuid.UUID  `json:"id"`
	OrganizationID      uuid.UUID  `json:"organizationId"`
	Kind                Kind       `json:"kind"`
	CounterpartyPattern string     `json:"counterpartyPattern"`
	RequiredAttributes  []string   `json:"requiredAttributes"`
	Effect              Effect     `json:"effect"`
	ApproveSubset       []string   `json:"approveSubset"`
	FourEyes            bool       `json:"fourEyes"`
	Priority            int        `json:"priority"`
	CreatedBy           *uuid.UUID `json:"createdBy"`
	ValidFrom           *time.Time `json:"validFrom"`
	ValidUntil          *time.Time `json:"validUntil"`
	RevokedAt           *time.Time `json:"revokedAt"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

// PolicySpec is the authorable content of a policy (everything but its identity,
// provenance timestamps and revocation, which the store owns).
type PolicySpec struct {
	Kind                Kind
	CounterpartyPattern string
	RequiredAttributes  []string
	Effect              Effect
	ApproveSubset       []string // empty = the full requested set (auto_approve only)
	FourEyes            bool
	Priority            int
	ValidFrom           *time.Time
	ValidUntil          *time.Time
}

func (s PolicySpec) validate() error {
	if !s.Kind.valid() {
		return ErrInvalidKind
	}
	if !s.Effect.valid() {
		return ErrInvalidEffect
	}
	if s.CounterpartyPattern == "" {
		return ErrEmptyCounterparty
	}
	return nil
}

var (
	ErrInvalidKind       = errors.New("consent: invalid kind")
	ErrInvalidEffect     = errors.New("consent: invalid effect")
	ErrEmptyCounterparty = errors.New("consent: empty counterparty pattern")
	ErrNoAttributes      = errors.New("consent: request has no attributes")

	ErrRequestNotFound = errors.New("consent: approval request not found")
	ErrRequestResolved = errors.New("consent: approval request already resolved")
	ErrEmptySubset     = errors.New("consent: approved subset is empty")
	ErrSubsetNotSubset = errors.New("consent: approved subset is not a subset of the requested attributes")

	// Four-eyes.
	ErrNotDualControl         = errors.New("consent: request is not four-eyes")
	ErrAwaitingFirstApproval  = errors.New("consent: request has no first approval yet")
	ErrAwaitingSecondApproval = errors.New("consent: request already has its first approval; complete it as the second approver")
	ErrSameApprover           = errors.New("consent: the second approver must be a different subject")

	ErrPolicyNotFound = errors.New("consent: policy not found")
	ErrPolicyRevoked  = errors.New("consent: policy is revoked")
)
