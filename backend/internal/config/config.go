package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	envDatabaseURL = "DATABASE_URL"
	envLogLevel    = "LOG_LEVEL"
	envLogFormat   = "LOG_FORMAT"
	envLogSource   = "LOG_SOURCE"

	envEudiVerifierURL     = "EUDI_VERIFIER_URL"
	envEudiIssuerChain     = "EUDI_ISSUER_CHAIN"
	envSessionCookieSecure = "SESSION_COOKIE_SECURE"
	envSessionTTL          = "SESSION_TTL"
	envSessionPruneEvery   = "SESSION_PRUNE_INTERVAL"
	envPresentationTTL     = "PRESENTATION_SESSION_TTL"

	envPlatformAdminEmails = "PLATFORM_ADMIN_EMAILS"

	envQerdsProvider             = "QERDS_PROVIDER"
	envQerdsProviderURL          = "QERDS_PROVIDER_URL"
	envQerdsAuthToken            = "QERDS_AUTH_TOKEN"
	envQerdsWebhookSecret        = "QERDS_WEBHOOK_SECRET"
	envQerdsDefaultAddressDomain = "QERDS_DEFAULT_ADDRESS_DOMAIN"

	envWalletRegistryProvider = "WALLET_REGISTRY_PROVIDER"

	// Attestation issuance (OpenID4VCI). The hosted Veramo issuer is addressed per
	// instance and authenticated with a Bearer admin token; the ping credential is
	// offered by the boot probe to validate URL + token + a configured credential.
	envAttestationIssuer         = "ATTESTATION_ISSUER"
	envAttestationIssuerURL      = "ATTESTATION_ISSUER_URL"
	envAttestationIssuerToken    = "ATTESTATION_ISSUER_ADMIN_TOKEN"
	envAttestationIssuerInstance = "ATTESTATION_ISSUER_INSTANCE"
	envAttestationPingCredential = "ATTESTATION_ISSUER_PING_CREDENTIAL"

	// Attestation holder (the "store, select" side). The irmago EUDI holder engine
	// is backed by Postgres, one isolated schema per org; the storage dir holds
	// irmago's per-org filesystem material and the master key (hex 32 bytes) seeds
	// per-org key derivation. Both are required only when the irmago engine is
	// selected (the stub needs neither).
	envAttestationHolder           = "ATTESTATION_HOLDER"
	envAttestationHolderStorageDir = "ATTESTATION_HOLDER_STORAGE_DIR"
	envAttestationHolderMasterKey  = "ATTESTATION_HOLDER_MASTER_KEY"
	// Trust posture for the holder's OpenID4VCI receive/redeem path (QERDS).
	envAttestationHolderTrustChain        = "ATTESTATION_HOLDER_TRUST_CHAIN"
	envAttestationHolderStagingAnchors    = "ATTESTATION_HOLDER_STAGING_ANCHORS"
	envAttestationHolderAllowInsecureHTTP = "ATTESTATION_HOLDER_ALLOW_INSECURE_HTTP"

	// APP_BASE_URL is the public base URL of the frontend, used to build links in
	// outbound e-mail / QERDS messages (e.g. the credential claim page).
	envAppBaseURL = "APP_BASE_URL"
	// EMAIL_ENCRYPTION_KEY (hex 32 bytes) encrypts per-org SMTP passwords at rest.
	envEmailEncryptionKey = "EMAIL_ENCRYPTION_KEY"
	// STATIC_DIR points at the built frontend; when set the API also serves it as
	// an SPA on "/". Unset in dev (Vite serves the frontend).
	envStaticDir = "STATIC_DIR"

	defaultAppBaseURL = "http://localhost:5173"

	// PostGuard: the internal sidecar that performs encrypt-and-upload, the shared
	// secret the backend presents to it, and the deployment master key that wraps
	// each org's own (owner-configured) encryption key at rest (envelope
	// encryption); the per-org key in turn encrypts that org's API key.
	envPostGuardSidecarURL    = "POSTGUARD_SIDECAR_URL"
	envPostGuardSharedSecret  = "POSTGUARD_SHARED_SECRET"
	envPostGuardEncryptionKey = "POSTGUARD_KEY_ENCRYPTION_KEY"

	// Domibus WS-plugin ebMS3 addressing. Defaults match the parties in the
	// Domibus sample PMode so a blue -> red self-send works out of the box.
	envQerdsDomibusFromParty   = "QERDS_DOMIBUS_FROM_PARTY"
	envQerdsDomibusToParty     = "QERDS_DOMIBUS_TO_PARTY"
	envQerdsDomibusPartyType   = "QERDS_DOMIBUS_PARTY_ID_TYPE"
	envQerdsDomibusService     = "QERDS_DOMIBUS_SERVICE"
	envQerdsDomibusServiceType = "QERDS_DOMIBUS_SERVICE_TYPE"
	envQerdsDomibusAction      = "QERDS_DOMIBUS_ACTION"

	defaultLogLevel  = "info"
	defaultLogFormat = "text"
	defaultLogSource = "true"

	// The hosted EUDI reference Verifier Endpoint (Yivi staging). Overridable so a
	// deployment can point at its own verifier.
	defaultEudiVerifierURL     = "https://verifierapi.openid4vc.staging.yivi.app"
	defaultSessionCookieSecure = "false"
	defaultSessionTTL          = "24h"
	defaultSessionPruneEvery   = "1h"
	// A login/disclosure flow (scan QR, present in the wallet, claim) completes in
	// minutes; the presentation-session mapping only needs to outlive that window.
	defaultPresentationTTL = "15m"

	// ProviderStub selects the in-process StubProvider (local dev / CI).
	ProviderStub = "stub"
	// ProviderDomibus selects the Domibus AS4 access-point driver. Requires
	// QERDS_PROVIDER_URL (the WS-plugin endpoint).
	ProviderDomibus = "domibus"
	// IssuerStub selects the in-process StubIssuer (local dev / CI); IssuerVeramo
	// selects the hosted Veramo OpenID4VCI issuer.
	IssuerStub   = "stub"
	IssuerVeramo = "veramo"

	defaultAttestationIssuer = IssuerStub

	// HolderStub selects the in-process StubHolder (local dev / CI); HolderIrmago
	// selects the irmago EUDI holder engine backed by Postgres.
	HolderStub   = "stub"
	HolderIrmago = "irmago"

	defaultAttestationHolder = HolderStub

	defaultQerdsProvider             = ProviderStub
	defaultQerdsDefaultAddressDomain = "qerds.localhost"

	// The wallet-bootstrap registry (KVK) provider. Reuses ProviderStub ("stub").
	defaultWalletRegistryProvider = ProviderStub

	defaultQerdsDomibusFromParty   = "domibus-blue"
	defaultQerdsDomibusToParty     = "domibus-red"
	defaultQerdsDomibusPartyType   = "urn:oasis:names:tc:ebcore:partyid-type:unregistered"
	defaultQerdsDomibusService     = "bdx:noprocess"
	defaultQerdsDomibusServiceType = "tc1"
	defaultQerdsDomibusAction      = "TC1Leg1"
)

type Config struct {
	DatabaseDSN string
	LogLevel    string
	LogFormat   string
	LogSource   bool

	EudiVerifierURL     string
	EudiIssuerChain     string
	SessionCookieSecure bool
	SessionTTL          time.Duration
	SessionPruneEvery   time.Duration
	PresentationTTL     time.Duration

	QerdsProvider             string
	QerdsProviderURL          string
	QerdsAuthToken            string
	QerdsWebhookSecret        string
	QerdsDefaultAddressDomain string

	QerdsDomibusFromParty   string
	QerdsDomibusToParty     string
	QerdsDomibusPartyType   string
	QerdsDomibusService     string
	QerdsDomibusServiceType string
	QerdsDomibusAction      string

	WalletRegistryProvider string

	AttestationIssuer         string
	AttestationIssuerURL      string
	AttestationIssuerToken    string
	AttestationIssuerInstance string
	AttestationPingCredential string

	AttestationHolder           string
	AttestationHolderStorageDir string
	AttestationHolderMasterKey  string
	// AttestationHolderTrustChain is the trusted-issuer CA PEM the holder verifies
	// received credentials against (holder analogue of EudiIssuerChain). Empty uses
	// irmago's built-in trust model.
	AttestationHolderTrustChain string
	// AttestationHolderStagingAnchors adds irmago's staging trust anchors (for the
	// Yivi staging Veramo issuer in dev/staging).
	AttestationHolderStagingAnchors bool
	// AttestationHolderAllowInsecureHTTP permits http:// issuer endpoints on the
	// receive path (local dev only).
	AttestationHolderAllowInsecureHTTP bool

	AppBaseURL         string
	EmailEncryptionKey string

	// StaticDir is the directory holding the built frontend (index.html + assets).
	// When set, the API server also serves it as an SPA on "/"; empty disables
	// static serving (dev serves the frontend via Vite).
	StaticDir string

	PostGuardSidecarURL    string
	PostGuardSharedSecret  string
	PostGuardEncryptionKey string

	PlatformAdminEmails []string
}

func Load() (Config, error) {
	dsn := os.Getenv(envDatabaseURL)
	if dsn == "" {
		return Config{}, fmt.Errorf("%s is required", envDatabaseURL)
	}

	cookieSecure := strings.EqualFold(envOrDefault(envSessionCookieSecure, defaultSessionCookieSecure), "true")

	verifierURL := envOrDefault(envEudiVerifierURL, defaultEudiVerifierURL)
	if cookieSecure && os.Getenv(envEudiVerifierURL) == "" {
		return Config{}, fmt.Errorf("config: %s must be set when %s is true", envEudiVerifierURL, envSessionCookieSecure)
	}

	sessionTTL, err := parseDuration(envSessionTTL, defaultSessionTTL)
	if err != nil {
		return Config{}, err
	}

	sessionPruneEvery, err := parseDuration(envSessionPruneEvery, defaultSessionPruneEvery)
	if err != nil {
		return Config{}, err
	}

	presentationTTL, err := parseDuration(envPresentationTTL, defaultPresentationTTL)
	if err != nil {
		return Config{}, err
	}

	qerdsProvider := envOrDefault(envQerdsProvider, defaultQerdsProvider)
	qerdsProviderURL := os.Getenv(envQerdsProviderURL)
	if qerdsProvider != ProviderStub && qerdsProviderURL == "" {
		return Config{}, fmt.Errorf("config: %s must be set when %s is not %q", envQerdsProviderURL, envQerdsProvider, ProviderStub)
	}

	attestationIssuer := envOrDefault(envAttestationIssuer, defaultAttestationIssuer)
	attestationIssuerURL := os.Getenv(envAttestationIssuerURL)
	attestationIssuerInstance := os.Getenv(envAttestationIssuerInstance)
	if attestationIssuer != IssuerStub {
		if attestationIssuerURL == "" {
			return Config{}, fmt.Errorf("config: %s must be set when %s is not %q", envAttestationIssuerURL, envAttestationIssuer, IssuerStub)
		}
		if attestationIssuerInstance == "" {
			return Config{}, fmt.Errorf("config: %s must be set when %s is not %q", envAttestationIssuerInstance, envAttestationIssuer, IssuerStub)
		}
	}

	attestationHolder := envOrDefault(envAttestationHolder, defaultAttestationHolder)
	attestationHolderStorageDir := os.Getenv(envAttestationHolderStorageDir)
	attestationHolderMasterKey := os.Getenv(envAttestationHolderMasterKey)
	if attestationHolder != HolderStub {
		if attestationHolderStorageDir == "" {
			return Config{}, fmt.Errorf("config: %s must be set when %s is not %q", envAttestationHolderStorageDir, envAttestationHolder, HolderStub)
		}
		if attestationHolderMasterKey == "" {
			return Config{}, fmt.Errorf("config: %s must be set when %s is not %q", envAttestationHolderMasterKey, envAttestationHolder, HolderStub)
		}
	}

	return Config{
		DatabaseDSN: dsn,
		LogLevel:    envOrDefault(envLogLevel, defaultLogLevel),
		LogFormat:   envOrDefault(envLogFormat, defaultLogFormat),
		LogSource:   strings.EqualFold(envOrDefault(envLogSource, defaultLogSource), "true"),

		EudiVerifierURL:     verifierURL,
		EudiIssuerChain:     os.Getenv(envEudiIssuerChain),
		SessionCookieSecure: cookieSecure,
		SessionTTL:          sessionTTL,
		SessionPruneEvery:   sessionPruneEvery,
		PresentationTTL:     presentationTTL,

		QerdsProvider:             qerdsProvider,
		QerdsProviderURL:          qerdsProviderURL,
		QerdsAuthToken:            os.Getenv(envQerdsAuthToken),
		QerdsWebhookSecret:        os.Getenv(envQerdsWebhookSecret),
		QerdsDefaultAddressDomain: envOrDefault(envQerdsDefaultAddressDomain, defaultQerdsDefaultAddressDomain),

		QerdsDomibusFromParty:   envOrDefault(envQerdsDomibusFromParty, defaultQerdsDomibusFromParty),
		QerdsDomibusToParty:     envOrDefault(envQerdsDomibusToParty, defaultQerdsDomibusToParty),
		QerdsDomibusPartyType:   envOrDefault(envQerdsDomibusPartyType, defaultQerdsDomibusPartyType),
		QerdsDomibusService:     envOrDefault(envQerdsDomibusService, defaultQerdsDomibusService),
		QerdsDomibusServiceType: envOrDefault(envQerdsDomibusServiceType, defaultQerdsDomibusServiceType),
		QerdsDomibusAction:      envOrDefault(envQerdsDomibusAction, defaultQerdsDomibusAction),

		WalletRegistryProvider: envOrDefault(envWalletRegistryProvider, defaultWalletRegistryProvider),

		AttestationIssuer:         attestationIssuer,
		AttestationIssuerURL:      attestationIssuerURL,
		AttestationIssuerToken:    os.Getenv(envAttestationIssuerToken),
		AttestationIssuerInstance: attestationIssuerInstance,
		AttestationPingCredential: os.Getenv(envAttestationPingCredential),

		AttestationHolder:           attestationHolder,
		AttestationHolderStorageDir: attestationHolderStorageDir,
		AttestationHolderMasterKey:  attestationHolderMasterKey,
		AttestationHolderTrustChain: os.Getenv(envAttestationHolderTrustChain),
		AttestationHolderStagingAnchors: strings.EqualFold(
			os.Getenv(envAttestationHolderStagingAnchors), "true"),
		AttestationHolderAllowInsecureHTTP: strings.EqualFold(
			os.Getenv(envAttestationHolderAllowInsecureHTTP), "true"),

		AppBaseURL:         envOrDefault(envAppBaseURL, defaultAppBaseURL),
		EmailEncryptionKey: os.Getenv(envEmailEncryptionKey),
		StaticDir:          os.Getenv(envStaticDir),

		PostGuardSidecarURL:    os.Getenv(envPostGuardSidecarURL),
		PostGuardSharedSecret:  os.Getenv(envPostGuardSharedSecret),
		PostGuardEncryptionKey: os.Getenv(envPostGuardEncryptionKey),

		PlatformAdminEmails: parseList(os.Getenv(envPlatformAdminEmails)),
	}, nil
}

func parseList(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func envOrDefault(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}

func parseDuration(key, fallback string) (time.Duration, error) {
	raw := envOrDefault(key, fallback)
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s %q: %w", key, raw, err)
	}
	return d, nil
}
