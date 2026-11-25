package attestation

import "time"

// Issuance lifecycle: offered -> claimed, with expired / failed. Two distinct
// terminal withdrawals: an unclaimed offer is cancelled, an already-claimed
// credential is revoked (the latter is the Token Status List-backed path).
// A receiving organization that turns an offer down makes it declined.
const (
	StatusOffered   = "offered"
	StatusClaimed   = "claimed"
	StatusExpired   = "expired"
	StatusRevoked   = "revoked"
	StatusCancelled = "cancelled"
	StatusFailed    = "failed"
	StatusDeclined  = "declined"
)

// Schema lifecycle.
const (
	SchemaDraft      = "draft"
	SchemaActive     = "active"
	SchemaDeprecated = "deprecated"
)

// Template lifecycle.
const (
	TemplateActive   = "active"
	TemplateArchived = "archived"
)

// Key material kinds and lifecycle.
const (
	KeyWalletManaged        = "wallet_managed"
	KeyQualifiedCertificate = "qualified_certificate"

	KeyActive    = "active"
	KeySuspended = "suspended"
	KeyRevoked   = "revoked"
)

// defaultExpirationSeconds is the issued-credential lifetime when a template sets
// no validity period (one year, matching the reference issuer).
const defaultExpirationSeconds = 31536000

// statusListRequiredAbove is the validity from which a credential needs a Token
// Status List (WE BUILD CS-10, EBWOID rulebook).
const statusListRequiredAbove = 24 * time.Hour

// expirationSecondsFor is the lifetime a template's validity gives a credential;
// zero or less means it never expires.
func expirationSecondsFor(validitySeconds *int) int {
	if validitySeconds == nil {
		return defaultExpirationSeconds
	}
	return *validitySeconds
}

// needsStatusList reports whether a credential issued with that validity lives
// past statusListRequiredAbove.
func needsStatusList(validitySeconds *int) bool {
	seconds := expirationSecondsFor(validitySeconds)
	return seconds <= 0 || time.Duration(seconds)*time.Second > statusListRequiredAbove
}
