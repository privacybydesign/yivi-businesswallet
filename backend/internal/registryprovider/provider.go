// Package registryprovider is the client seam to a business register (KVK, the
// NL Chamber of Commerce) acting as an authentic source under COM(2025) 838
// Art 9 / Art 6(1)(e). It mirrors internal/qerdsprovider: our backend is a
// requestor talking to an authentic source over a channel, never the register
// itself. The concrete provider is swapped by config — a StubRegistry in
// dev/CI, a real KVK/BRIS driver later. See .ai/features/wallet-bootstrap.md.
package registryprovider

import "time"

// AttestationContentType is the QERDS content type carrying a KVK registration
// attestation, recognised on the inbound webhook and dispatched to the wallet
// slice.
const AttestationContentType = "nl.kvk.registration-attestation.v1"

// Representation kinds and authority forms as asserted by the register.
const (
	KindBestuurder     = "bestuurder"     // director — owner-grade authority
	KindGevolmachtigde = "gevolmachtigde" // proxy / power of attorney — scoped
	KindOverig         = "overig"

	AuthoritySole    = "sole"
	AuthorityJointly = "jointly"
)

// PID is the requester's verified identity, disclosed via OpenID4VP (passport or
// id-card). It is presented to KVK so the register can match the natural person
// against its records — KVK, not us, decides authorisation. See
// .ai/features/auth-openid4vp.md.
type PID struct {
	GivenNames  string
	FamilyName  string
	DateOfBirth string
	Nationality string
}

// RegistrationRequest is the {PID, KVK number} sent to KVK over QERDS.
type RegistrationRequest struct {
	PID       PID
	KVKNumber string
}

// RequestReceipt is returned once the request has been accepted for delivery.
// The attestation itself arrives asynchronously over QERDS (inbound webhook).
type RequestReceipt struct {
	ProviderRef string
}

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
	// RequesterIsRepresentative reports whether KVK matched the requester's PID
	// to a representative; RequesterRepresentativeIndex is that representative's
	// position in Representatives (valid only when the flag is true).
	RequesterIsRepresentative    bool
	RequesterRepresentativeIndex int
	IssuedAt                     time.Time
}
