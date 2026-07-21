// Package openid4vciissuer is the client seam to a hosted OpenID4VCI issuer (the
// Veramo issuer, mirror of internal/openid4vpverifier for the issue side). Our
// backend is a requestor / orchestrator in front of the hosted issuer: it asks
// the issuer to create a credential offer (supplying the attribute values) and
// polls the issuance result. It does NOT hold the signing key, run the token
// endpoint, or perform the SD-JWT / proof-of-possession cryptography — the hosted
// issuer does all of that. See .ai/features/attestations.md.
package openid4vciissuer

import "errors"

// Issuance status values. The hosted issuer reports the offer as pending until
// the recipient's wallet completes the OpenID4VCI handshake, then issued.
const (
	StatusPending = "PENDING"
	StatusIssued  = "CREDENTIAL_ISSUED"
)

// ErrPending means the recipient has not claimed the credential yet.
var ErrPending = errors.New("openid4vciissuer: issuance pending")

// ErrNoStatusListBit means the issuer has no status-list bit reserved for the
// credential (it answered UNKNOWN): the deployment issues without a status list,
// or the list could not be found. Callers can treat this as a degrade-to-local
// signal rather than a hard failure — there is nothing published to flip, so
// revocation falls back to the local ledger alone (matching StubIssuer, which
// keeps no status list and returns a no-op success).
var ErrNoStatusListBit = errors.New("openid4vciissuer: no status-list bit reserved for the credential")

// IssuanceStatus reports an offer's progress. CredentialUUID is the issuer's
// handle for the minted credential (check-offer's `uuid`), populated only once
// Status is StatusIssued — it is what the revocation API keys on to flip the
// credential's bit on the issuer's Token Status List.
type IssuanceStatus struct {
	Status         string
	CredentialUUID string
}

// OfferRequest is what the attestation service asks the issuer to offer. Unlike
// the verify side (where the wallet discloses claims to us), here we push the
// attribute values into the offer; the issuer seals them into the credential.
type OfferRequest struct {
	// Instance is the issuer instance to create the offer at (the {instance}
	// path segment). Empty uses the client's configured default instance; a
	// per-organization instance routes the offer to that org's issuer.
	Instance string
	// CredentialConfigID is the issuer-registered credential type to issue (the
	// Veramo credentialId our schema maps to).
	CredentialConfigID string
	// Claims are the attribute values sealed into the credential
	// (credentialDataSupplierInput).
	Claims map[string]any
	// ExpirationSeconds is the issued credential's lifetime.
	ExpirationSeconds int
	// UseTxCode requests a short numeric PIN (tx_code) that the recipient's wallet
	// prompts for to complete the pre-authorized-code flow. Note: we currently
	// deliver the PIN together with the offer link (claim page / email / QERDS),
	// so it is a wallet-flow step rather than an out-of-band factor — the opaque
	// claim token remains the access boundary. Real recipient binding would
	// require sending the PIN over a genuinely separate channel.
	UseTxCode bool
}

// Offer is a created credential offer: the opaque issuance id we poll on plus the
// wallet deeplink to render as a QR / universal link, and an optional tx_code.
type Offer struct {
	IssuanceID string
	OfferURI   string
	TxCode     string
}
