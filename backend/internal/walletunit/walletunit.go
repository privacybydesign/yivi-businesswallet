// Package walletunit is the Business Wallet Provider side of the WE BUILD Business
// Wallet Unit Attestation (CS-05): it attests an organization's wallet unit with a
// BWIA and, for HSM-held keys, an SKA, publishes the revocation status of each as
// a Token Status List and revokes on the CS-05 triggers. See
// .ai/features/wallet-unit-attestation.md.
package walletunit

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Wallet unit lifecycle.
const (
	UnitActive  = "active"
	UnitRevoked = "revoked"
)

// Reasons an admin revokes a wallet unit for (CS-05 7.2 item 8).
const (
	ReasonOwnerRequest  = "owner_request"
	ReasonKeyCompromise = "key_compromise"
)

// Attestation kinds as stored.
const (
	KindBWIA = "bwia"
	KindSKA  = "ska"
)

const (
	bwiaTyp       = "oauth-client-attestation+jwt"
	skaTyp        = "key-attestation+jwt"
	statusListTyp = "statuslist+jwt"

	// tokenTTL is the technical validity of a BWIA and SKA; CS-05 7.1 wants it
	// under 24 hours and keeps them single-use.
	tokenTTL = time.Hour
	// statusMaintenance is how long an issued entry stays published. CS-05 7.2 wants
	// it at least 31 days ahead whenever the attestation is presented.
	statusMaintenance = 90 * 24 * time.Hour
)

var (
	ErrNoEUID         = errors.New("walletunit: organization has no EUID")
	ErrOrgNotActive   = errors.New("walletunit: organization is not active")
	ErrUnitRevoked    = errors.New("walletunit: wallet unit is revoked")
	ErrNoActiveUnit   = errors.New("walletunit: organization has no active wallet unit")
	ErrUnitActive     = errors.New("walletunit: organization already has an active wallet unit")
	ErrInvalidReason  = errors.New("walletunit: unknown revocation reason")
	ErrNoInstanceKey  = errors.New("walletunit: instance key is required")
	ErrUnknownList    = errors.New("walletunit: unknown status list")
	ErrIndexExhausted = errors.New("walletunit: no free status index")
)

// Unit is an organization's wallet unit (CS-05 BWU).
type Unit struct {
	ID            uuid.UUID  `json:"id"`
	Status        string     `json:"status"`
	RevokedReason *string    `json:"revokedReason,omitempty"`
	RevokedAt     *time.Time `json:"revokedAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// Summary is what an org admin sees of the latest wallet unit: its state and how
// many attestations were issued to it.
type Summary struct {
	Unit         Unit       `json:"unit"`
	IssuedCount  int        `json:"issuedCount"`
	LastIssuedAt *time.Time `json:"lastIssuedAt,omitempty"`
}

func validReason(reason string) bool {
	return reason == ReasonOwnerRequest || reason == ReasonKeyCompromise
}
