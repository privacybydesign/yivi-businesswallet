package attestation

// Issuance lifecycle: offered -> claimed, with expired / revoked / failed.
const (
	StatusOffered = "offered"
	StatusClaimed = "claimed"
	StatusExpired = "expired"
	StatusRevoked = "revoked"
	StatusFailed  = "failed"
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
