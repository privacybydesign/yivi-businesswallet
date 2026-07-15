// Package wallet is the domain slice that bootstraps a business wallet instance
// from a KVK registration attestation delivered over QERDS (COM(2025) 838
// Art 6(1)(e), Art 8, Art 9). It orchestrates the registry provider seam
// (internal/registryprovider), the qerds slice and the organization slice behind
// an org-scoped API. See .ai/features/wallet-bootstrap.md.
package wallet

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Instance lifecycle statuses.
const (
	StatusProvisioning        = "provisioning"
	StatusAwaitingAttestation = "awaiting_attestation"
	StatusActive              = "active"
	StatusRejected            = "rejected"
	StatusSuspended           = "suspended"
	StatusRevoked             = "revoked"
)

// Reject reasons recorded on wallet_instances.reject_reason.
const (
	RejectNotRepresentative = "not_a_representative"
)

var (
	ErrInstanceNotFound       = errors.New("wallet: instance not found")
	ErrRegistrationInProgress = errors.New("wallet: a registration is already in progress for this company")
	ErrRepresentationNotFound = errors.New("wallet: representation not found")
	// ErrNotImplemented marks scaffold seams not yet wired. Handlers map it to a
	// 501 so the API surface is discoverable while the flow is built out.
	ErrNotImplemented = errors.New("wallet: not implemented")
)

// Instance is a business wallet's lifecycle record. The organization identity
// fields (OrganizationID, LegalName, EUID) are populated only once KVK's
// attestation activates it — before that the wallet exists but its verified
// identity does not.
type Instance struct {
	ID             uuid.UUID  `json:"id"`
	Status         string     `json:"status"`
	KVKNumber      string     `json:"kvkNumber"`
	DigitalAddress string     `json:"digitalAddress"`
	OrganizationID *uuid.UUID `json:"organizationId,omitempty"`
	LegalName      string     `json:"legalName,omitempty"`
	EUID           string     `json:"euid,omitempty"`
	RejectReason   string     `json:"rejectReason,omitempty"`
	BootstrappedAt *time.Time `json:"bootstrappedAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

// Representation is one authorised representative from the KVK attestation — the
// legal mandate list (Art 5(1)(j), Art 6(2)). It is claimed when a person proves
// they are that representative (OpenID4VP), which grants them a membership.
type Representation struct {
	ID         uuid.UUID  `json:"id"`
	Kind       string     `json:"kind"`
	GivenNames string     `json:"givenNames"`
	FamilyName string     `json:"familyName"`
	Authority  string     `json:"authority"`
	Claimed    bool       `json:"claimed"`
	ClaimedAt  *time.Time `json:"claimedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
}
