// Package wallet registers and manages business wallets. An organization IS a
// business wallet: registration creates the organization (with KVK identity, a
// QERDS digital address and the owner) from a KVK attestation, and this slice
// also manages the mandate list and the wallet's lifecycle status. It orchestrates
// the registry provider seam (internal/registryprovider), the organization and
// qerds slices, and auth. See .ai/features/wallet-bootstrap.md.
package wallet

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

var (
	// ErrAlreadyRegistered means a wallet already exists for the KVK number.
	ErrAlreadyRegistered = errors.New("wallet: this company already has a business wallet")
	// ErrSlugTaken means the chosen slug is already in use.
	ErrSlugTaken = errors.New("wallet: slug already taken")
	// ErrNotRepresentative means KVK does not list the requester as a representative.
	ErrNotRepresentative = errors.New("wallet: requester is not a listed representative")
	// ErrUnknownKVK means the KVK number is not in the register at all.
	ErrUnknownKVK = errors.New("wallet: kvk number not found in the register")

	ErrRepresentationNotFound = errors.New("wallet: representation not found")
	// ErrNotImplemented marks scaffold seams not yet wired. Handlers map it to 501.
	ErrNotImplemented = errors.New("wallet: not implemented")
)

// Requester is the natural person opening a wallet, identified by their disclosed
// PID (verified name + date of birth). KVK matches it against the company's
// authorised representatives. DateOfBirth is "2006-01-02" and may be empty when
// the caller has no verified birth date (the logged-in "register another company"
// path carries only the stored name), in which case KVK matches on name alone.
type Requester struct {
	GivenNames  string
	FamilyName  string
	DateOfBirth string
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

// RegistrationResult is the outcome of registering a wallet: the created
// organization plus the requester's own representation (so the UI can show, e.g.,
// "you are registered as a beperkt volmacht").
type RegistrationResult struct {
	Organization            organization.Organization
	RepresentationKind      string
	RepresentationAuthority string
}
