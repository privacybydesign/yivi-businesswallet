// Package verification is the requester side of a business-wallet-to-business-
// wallet disclosure (issue #245): an organisation defines what it wants to see
// (a template), starts a presentation request for it at the hosted OpenID4VP
// verifier, shows the resulting link as a QR, polls for the disclosed claims,
// and grades them against its own issuance ledger. It reuses the login flow's
// requestor seam (internal/openid4vpverifier + a server-side transaction id)
// and adds nothing to the holder side, which .ai/features/openid4vp-inbound.md
// covers. See .ai/features/verification-templates.md.
package verification

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Session statuses. pending and completed are stored; expired is derived on
// read from expires_at, like openid4vppresenter.Transaction.EffectiveStatus.
const (
	StatusPending   = "pending"
	StatusCompleted = "completed"
	StatusExpired   = "expired"
)

// Check names, in the order a result card lists them. Each is graded once, when
// the disclosure arrives, and stored with the session.
const (
	// CheckVerified: the hosted verifier accepted the presentation, which means it
	// verified the issuer signature, trust chain and key binding (the package doc
	// of openid4vpverifier). It fails only when the verifier returned nothing
	// under the requested credential id.
	CheckVerified = "verified"
	// CheckNotExpired: the credential's issuer-set expiry (or, when it carries
	// none, the ledger's) lies in the future.
	CheckNotExpired = "not_expired"
	// CheckIssuedHere: the disclosed claims match a row in this organisation's
	// own issuance ledger for the same credential type.
	CheckIssuedHere = "issued_here"
	// CheckNotRevoked: that ledger row has not been revoked.
	CheckNotRevoked = "not_revoked"
)

var (
	ErrTemplateNotFound = errors.New("verification: template not found")
	ErrSessionNotFound  = errors.New("verification: session not found")
	// ErrVerifierUnavailable: the hosted verifier refused or failed to start the
	// presentation request.
	ErrVerifierUnavailable = errors.New("verification: verifier unavailable")
)

// Template is an admin-managed presentation request preset: the credential type
// to ask a holder for, the claims to disclose (all required), and the purpose.
type Template struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organizationId"`
	Name           string    `json:"name"`
	VCT            string    `json:"vct"`
	Claims         []string  `json:"claims"`
	Purpose        string    `json:"purpose"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// Check is one graded outcome of a completed session.
type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	// Detail is a short machine-readable reason for a failure (a ledger status,
	// an expiry date), empty when the check passed.
	Detail string `json:"detail,omitempty"`
}

// Session is one verification run from a template. TransactionID is the hosted
// verifier's handle and never leaves the backend; the browser polls by ID.
type Session struct {
	ID              uuid.UUID         `json:"id"`
	OrganizationID  uuid.UUID         `json:"organizationId"`
	TemplateID      *uuid.UUID        `json:"templateId,omitempty"`
	TemplateName    string            `json:"templateName"`
	VCT             string            `json:"vct"`
	TransactionID   string            `json:"-"`
	WalletLink      string            `json:"-"`
	Status          string            `json:"status"`
	StartedByUserID *uuid.UUID        `json:"startedByUserId,omitempty"`
	Claims          map[string]string `json:"claims,omitempty"`
	Checks          []Check           `json:"checks,omitempty"`
	// Valid is whether every check passed; nil until the session completes.
	Valid       *bool      `json:"valid,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// EffectiveStatus applies the expiry on read: a pending session past expires_at
// reports expired without a write, so a reloaded page and the history agree.
func (s Session) EffectiveStatus(now time.Time) string {
	if s.Status == StatusPending && !now.Before(s.ExpiresAt) {
		return StatusExpired
	}
	return s.Status
}

// Result is a completed session's grading: the disclosed claims and the checks.
type Result struct {
	Claims map[string]string
	Checks []Check
	Valid  bool
}
