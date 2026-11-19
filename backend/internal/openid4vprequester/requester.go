// Package openid4vprequester is the sending side of org-to-org credential
// disclosure over QERDS (issue #271): an organization, acting as OpenID4VP
// relying party under its own certified identity, asks another organization's
// business wallet for credentials. It signs the Authorization Request, sends the
// invocation over QERDS, serves the Request Object once from request_uri,
// receives the direct_post answer at response_uri, verifies it and keeps what
// was disclosed. The receiving side is internal/openid4vppresenter. See
// .ai/features/oid4vp-over-qerds.md.
package openid4vprequester

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Status of an outbound request. StatusExpired is never stored: EffectiveStatus
// derives it from a sent request whose window has closed.
const (
	StatusSent      = "sent"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusExpired   = "expired"
)

// Failure reasons recorded on a request whose answer was refused. The holder's
// answer arrived with the right state but cannot be accepted, so the request is
// consumed: the nonce was used.
const (
	ReasonDeliveryFailed     = "qerds_send_failed"
	ReasonVerificationFailed = "verification_failed"
	ReasonIncompleteResponse = "incomplete_response"
)

var (
	ErrInvalidInput    = errors.New("invalid_input")
	ErrNotFound        = errors.New("request_not_found")
	ErrNotPending      = errors.New("request_not_pending")
	ErrInvalidResponse = errors.New("invalid_response")
	ErrDeliveryFailed  = errors.New("delivery_failed")
)

// CredentialRequest is one credential asked for: an SD-JWT VC of type VCT
// disclosing the named top-level claims. ID is assigned by the service and is
// the DCQL credential query id the answer is keyed by.
type CredentialRequest struct {
	ID     string   `json:"id"`
	VCT    string   `json:"vct"`
	Claims []string `json:"claims"`
}

// DisclosedCredential is one verified presentation from the answer.
type DisclosedCredential struct {
	QueryID string         `json:"queryId"`
	VCT     string         `json:"vct"`
	Issuer  string         `json:"issuer"`
	Claims  map[string]any `json:"claims"`
}

// Request is one outbound presentation request.
type Request struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	CreatedBy        *uuid.UUID
	SenderAddress    string
	RecipientAddress string
	QerdsMessageID   *uuid.UUID
	ClientID         string
	Nonce            string
	State            string
	Credentials      []CredentialRequest
	RequestObject    string
	RequestFetchedAt *time.Time
	Status           string
	FailureReason    *string
	Disclosed        []DisclosedCredential
	CreatedAt        time.Time
	ExpiresAt        time.Time
	RespondedAt      *time.Time
}

// EffectiveStatus is the stored status with expiry applied on read.
func (r Request) EffectiveStatus(now time.Time) string {
	if r.Status == StatusSent && now.After(r.ExpiresAt) {
		return StatusExpired
	}
	return r.Status
}
