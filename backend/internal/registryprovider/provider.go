// Package registryprovider is the client seam to a business register (KVK, the
// NL Chamber of Commerce) acting as an authentic source under COM(2025) 838
// Art 9 / Art 6(1)(e). It mirrors internal/qerdsprovider: our backend is a
// requestor talking to an authentic source, never the register itself. The
// concrete provider is swapped by config — a StubRegistry in dev/CI, a real
// KVK/BRIS driver later. See .ai/features/wallet-bootstrap.md.
package registryprovider

import (
	"errors"
	"time"
)

// ErrUnknownKVK means the consulted KVK number is not in the register at all —
// distinct from a known company whose requester simply is not a representative.
// The register did not validate the request; no wallet may be opened.
var ErrUnknownKVK = errors.New("registryprovider: kvk number not found in the register")

// ConsultRequest is what the requestor sends KVK: the KVK number of the company
// and the requester's identification data (from their disclosed PID) that KVK
// matches against the authorised representatives in its register. DateOfBirth is
// "2006-01-02" and may be empty when the caller has no verified birth date.
type ConsultRequest struct {
	KVKNumber   string
	GivenNames  string
	FamilyName  string
	DateOfBirth string
}

// AttestationContentType is the QERDS content type a real KVK attestation would
// carry when delivered over QERDS (the faithful, async transport). The stub
// consults synchronously, but the type is kept for the eventual inbound path.
const AttestationContentType = "nl.kvk.registration-attestation.v1"

// Representation kinds as asserted by the register.
const (
	KindBestuurder     = "bestuurder"     // director
	KindGevolmachtigde = "gevolmachtigde" // holder of a power of attorney (volmacht)
	KindOverig         = "overig"
)

// Authority describes the scope of a representative's mandate: for a bestuurder
// whether they act alone or jointly, and for a gevolmachtigde whether the volmacht
// is limited (beperkt) or full (volledig).
const (
	AuthoritySole     = "sole"
	AuthorityJointly  = "jointly"
	AuthorityBeperkt  = "beperkt"
	AuthorityVolledig = "volledig"
)

// Representative is one authorised representative as asserted by KVK.
type Representative struct {
	Kind        string
	GivenNames  string
	FamilyName  string
	DateOfBirth string
	Authority   string
}

// RegistrationAttestation is KVK's reply: the organization identity plus the
// authorised representatives, and whether the requester is among them.
type RegistrationAttestation struct {
	KVKNumber       string
	LegalName       string
	EUID            string
	Representatives []Representative
	// RequesterIsRepresentative reports whether the register lists the requester;
	// RequesterRepresentativeIndex is that representative's position in
	// Representatives (valid only when the flag is true).
	RequesterIsRepresentative    bool
	RequesterRepresentativeIndex int
	IssuedAt                     time.Time
}
