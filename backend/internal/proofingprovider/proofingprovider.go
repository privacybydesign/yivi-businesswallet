// Package proofingprovider is the leaf client seam for the identity-proofing
// service (privacybydesign/identity-proofing-service, "IPS"): document + face
// verification run on the subject's phone in the vcmrtd app. The business wallet
// is an IPS relying party, one IPS tenant per organization.
//
// This package imports no other internal/* package (leaf level, like
// signingprovider). It exports value types, a concrete net/http Client and an
// in-process Stub; the domain orchestration lives in internal/proofing behind a
// consumer-defined interface there. IPS's wire format is kept in this package
// only, because IPS is still moving (its device-binding API is work in progress).
//
// Redaction: a call carries the IPS admin key, a tenant API key or a session
// bearer token, and net/http names the URL in transport errors. No error from
// this package repeats the base URL or a credential; a failed call is reported
// as a status code, plus IPS's own validation message for a rejected request.
package proofingprovider

import (
	"errors"
	"fmt"
	"time"
)

// Scopes the wallet asks for on an org's IPS API key: sessions for proofing
// requests, flows for the admin flow screen. Nothing for audit, usage or webhooks.
var TenantKeyScopes = []string{"sessions:read", "sessions:write", "flows:read", "flows:manage"}

// Tenant is a freshly created IPS tenant. WebhookSecret is only ever returned by
// the create call.
type Tenant struct {
	ID            string
	WebhookSecret string
}

// FlowSpec is everything an org admin sets on a flow: the body of both a new
// flow and a new version of one. IPS validates the combination (which steps need
// which checks, what an assurance level requires) and answers a *RejectedError.
// Empty optional fields fall back to the tenant's own policy at IPS.
type FlowSpec struct {
	Name  string   `json:"name"`
	Steps []string `json:"steps"`
	// RequestedAttributes limits what a session result may carry (dg1, dg11, dg2,
	// selfie, chip_checks, biometrics, document_image). Nil lets IPS derive them
	// from the steps. The wallet never reads the data itself, only the outcome.
	RequestedAttributes []string `json:"requestedAttributes,omitempty"`
	// SelfieLocation is which client captures the face: "native" (the vcmrtd
	// app) or "browser". IPS defaults an empty value to browser.
	SelfieLocation           string             `json:"selfieLocation,omitempty"`
	AcceptedDocumentTypes    []string           `json:"acceptedDocumentTypes,omitempty"`
	AcceptedIssuingCountries []string           `json:"acceptedIssuingCountries,omitempty"`
	RequiredChecks           []string           `json:"requiredChecks,omitempty"`
	CheckThresholds          map[string]float64 `json:"checkThresholds,omitempty"`
	RequiredAssuranceLevel   string             `json:"requiredAssuranceLevel,omitempty"`
	// BSNPolicy is retrieve, mask or omit; empty inherits the tenant's.
	BSNPolicy string `json:"bsnPolicy,omitempty"`
	// BlurFace and BlurBSN are nil to inherit the tenant's redaction policy.
	BlurFace                 *bool           `json:"blurFace,omitempty"`
	BlurBSN                  *bool           `json:"blurBsn,omitempty"`
	RetentionOverrideSeconds int             `json:"retentionOverrideSeconds,omitempty"`
	AssuranceTiers           []AssuranceTier `json:"assuranceTiers,omitempty"`
	LegalBasis               string          `json:"legalBasis,omitempty"`
	ProcessingPurpose        string          `json:"processingPurpose,omitempty"`
}

// AssuranceTier maps a minimum score percentage to an IPS assurance tier name.
type AssuranceTier struct {
	Level      string  `json:"level"`
	MinPercent float64 `json:"minPercent"`
}

// Flow is one IPS flow version. The id is stable across versions; exactly one
// version of a flow is active, and a session pins the version active when it
// is created.
type Flow struct {
	FlowSpec
	ID        string    `json:"id"`
	Version   int       `json:"version"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"createdAt"`
}

// SessionInput starts a proofing session. ClientReference is echoed back by IPS
// on every read and webhook; the wallet sets it to its own request id. TTL is
// how long the session may live; IPS caps it at its own maximum (15 minutes).
type SessionInput struct {
	FlowID          string
	ClientReference string
	Language        string
	TTL             time.Duration
}

// Session is a created IPS session. Token is the relying-party bearer token every
// later read needs; IPS returns it only here.
type Session struct {
	ID        string
	Token     string
	ExpiresAt time.Time
	// Claim is the vcmrtd link for the subject's phone, nil when IPS offered none.
	Claim *Claim
}

// Claim is a single-use link that binds the subject's phone to a session. It is
// short-lived (minutes), so the page showing it asks for a fresh one when it lapses.
type Claim struct {
	DeepLink  string
	ExpiresAt time.Time
}

// Status is an IPS session status.
type Status string

const (
	StatusCreated     Status = "created"
	StatusOpened      Status = "opened"
	StatusInProgress  Status = "in_progress"
	StatusNeedsReview Status = "needs_review"
	StatusApproved    Status = "approved"
	StatusRejected    Status = "rejected"
	StatusExpired     Status = "expired"
	StatusCancelled   Status = "cancelled"
)

// Result is a session's outcome, reduced to the assurance summary. The IPS
// result also carries document fields, the BSN and images; they are never decoded.
type Result struct {
	Status         Status
	ErrorCode      string
	AssuranceLevel string
	EIDASLevel     string
	CompletedAt    *time.Time
}

// ErrNotFound is IPS answering 404: an unknown session, or a tenant it no longer has.
var ErrNotFound = errors.New("proofingprovider: not found")

// RejectedError is IPS refusing a request as invalid (400/409/422). Message is
// IPS's own explanation (e.g. which check a flow step requires) and is safe to
// show to the org admin who made the request.
type RejectedError struct {
	Status  int
	Message string
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("proofingprovider: rejected (status %d): %s", e.Status, e.Message)
}
