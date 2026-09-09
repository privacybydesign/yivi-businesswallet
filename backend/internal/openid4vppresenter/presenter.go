// Package openid4vppresenter is the inbound side of OpenID4VP: an external
// verifier invokes the business wallet itself as the holder/presenter of an
// organization's credentials. It is the protocol-role opposite of
// internal/openid4vpverifier, which fronts a hosted verifier so *this* backend
// can ask a natural person's device wallet for a login disclosure.
//
// The slice owns the invocation and routing seam (issue #188): one stable HTTPS
// entry point per deployment (GET /openid4vp, served by the SPA), an opaque
// transaction lifecycle persisted in openid4vp_transactions, organization
// selection gated behind the real tenant seam, a published wallet-metadata
// document, and the guarded fetch of the verifier's Request Object. It hands off
// to eudiholder.Holder.Present for the presentation crypto (#112) and leaves the
// consent/approval policy — who may let a selected organization's presentation go
// out — to the governance layer (#113). Until that layer lands, completing a
// presentation right after organization selection is a dev-only flag, never a
// default. See .ai/features/openid4vp-inbound.md.
package openid4vppresenter

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Transaction statuses, in lifecycle order. Code-defined like audit's action
// constants; the column is TEXT but never free text.
const (
	// StatusPendingAuth: the Request Object is fetched, validated and stored; the
	// browser still has to authenticate and pick an organization.
	StatusPendingAuth = "pending_auth"
	// StatusOrgSelected: an authenticated member chose an organization; the
	// presentation now waits for the governance decision (#113) — or, under the
	// dev-only auto-present flag, has been sent.
	StatusOrgSelected = "org_selected"
	// StatusCompleted: the Authorization Response was delivered to the verifier.
	StatusCompleted = "completed"
	// StatusDenied: the presentation was refused — by validation, by the
	// governance gate, or because building/sending it failed.
	StatusDenied = "denied"
	// StatusExpired: the TTL elapsed before the flow finished.
	StatusExpired = "expired"
)

var (
	// ErrNotFound covers an unknown transaction id. Like the outbound presentation
	// store it does not distinguish "never existed" from "pruned".
	ErrNotFound = errors.New("openid4vppresenter: unknown transaction")
	// ErrNotPending is returned when a transaction is acted on past the state the
	// action expects: already selected, completed, denied, or expired. It is what
	// enforces one-time use.
	ErrNotPending = errors.New("openid4vppresenter: transaction is not pending")
	// ErrForbidden is returned when a transaction bound to one authenticated user
	// is resumed by another.
	ErrForbidden = errors.New("openid4vppresenter: transaction belongs to another user")

	// ErrInvalidRequest is an OAuth invalid_request: the invocation parameters are
	// malformed or an unsupported/ambiguous combination.
	ErrInvalidRequest = errors.New("invalid_request")
	// ErrInvalidRequestURIMethod is OpenID4VP's invalid_request_uri_method: the
	// value is neither "get" nor "post".
	ErrInvalidRequestURIMethod = errors.New("invalid_request_uri_method")
	// ErrInvalidRequestObject: the fetched Request Object failed validation.
	ErrInvalidRequestObject = errors.New("invalid_request_object")
	// ErrRequestURIUnreachable: the request_uri could not be fetched within the
	// guards (scheme, network, redirect, size, timeout) or answered non-2xx.
	ErrRequestURIUnreachable = errors.New("request_uri_unreachable")
	// ErrValidationUnavailable: this deployment has no Request Object validator
	// it is allowed to trust (see RefusingValidator).
	ErrValidationUnavailable = errors.New("request_object_validation_unavailable")
	// ErrPresentationFailed: building or delivering the Authorization Response
	// failed; the transaction is consumed (denied).
	ErrPresentationFailed = errors.New("presentation_failed")
)

// Transaction is one inbound Authorization Request's persisted state machine.
// The raw opaque id is never stored — only its hash — so a row cannot be turned
// back into the bearer the browser holds.
type Transaction struct {
	ID               uuid.UUID
	ClientID         string
	RequestURI       string
	RequestURIMethod string
	VerifierIdentity string
	DCQLQuery        json.RawMessage
	Nonce            string
	State            string
	ResponseURI      string
	ResponseMode     string
	Status           string
	UserID           *uuid.UUID
	OrganizationID   *uuid.UUID
	ExpiresAt        time.Time
	ConsumedAt       *time.Time
}

// EffectiveStatus is the stored status with expiry applied: an unconsumed row
// past expires_at reads as expired even before the pruner marks it, so a resumed
// browser is told the truth without a write on a GET.
func (t Transaction) EffectiveStatus(now time.Time) string {
	if t.ConsumedAt == nil && now.After(t.ExpiresAt) {
		return StatusExpired
	}
	return t.Status
}

// RequestObject is the validated content of a signed OpenID4VP Authorization
// Request (a JAR, RFC 9101) — the subset this slice persists and later answers.
type RequestObject struct {
	ClientID string
	// VerifierIdentity is the human-readable "who is asking", resolved from the
	// client_id binding (for x509_san_dns: the DNS name).
	VerifierIdentity string
	Nonce            string
	State            string
	ResponseURI      string
	ResponseMode     string
	DCQLQuery        json.RawMessage
}
