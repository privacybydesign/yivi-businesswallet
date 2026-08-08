package config

import (
	"fmt"
	"net/url"
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
	// Deployment key that seals each org's WSCA activation secret at rest
	// (hex-encoded 32 bytes). Source from a KMS/secret manager. See
	// .ai/features/wsca-holder-binding.md.
	envAttestationHolderWSCAKEK = "ATTESTATION_HOLDER_WSCA_KEK"
	// WSCA (wallet-provider) holder-binding backend. When URL is set, redemption
	// binds holder keys via the WSCA/HSM instead of software keys.
	envAttestationHolderWSCAURL         = "ATTESTATION_HOLDER_WSCA_URL"
	envAttestationHolderWSCAKeystoreDir = "ATTESTATION_HOLDER_WSCA_KEYSTORE_DIR"
	envAttestationHolderWSCAInsecure    = "ATTESTATION_HOLDER_WSCA_INSECURE"

	// APP_BASE_URL is the public base URL of the frontend, used to build links in
	// outbound e-mail / QERDS messages (e.g. the credential claim page). Validated
	// as an absolute http(s) URL at load, because those links are built by
	// concatenation and internal/email refuses to render a relative one.
	envAppBaseURL = "APP_BASE_URL"
	// EMAIL_ENCRYPTION_KEY (hex 32 bytes) encrypts per-org SMTP passwords at rest.
	envEmailEncryptionKey = "EMAIL_ENCRYPTION_KEY"
	// SLACK_ENCRYPTION_KEY (hex 32 bytes) encrypts per-org Slack incoming-webhook
	// URLs at rest. A key of its own, like every other secret at rest here, so it
	// can be rotated without touching stored SMTP passwords; without it an org
	// cannot store a webhook URL at all (internal/slackchannel).
	envSlackEncryptionKey = "SLACK_ENCRYPTION_KEY"
	// TEAMS_ENCRYPTION_KEY (hex 32 bytes) encrypts per-org Microsoft Teams webhook
	// URLs at rest. Its own key rather than the Slack one, for the same reason every
	// other secret at rest here has one: the two are rotated by different decisions,
	// and rotating one must not take out the other channel. Without it an org cannot
	// store a Teams webhook URL at all (internal/teamschannel).
	envTeamsEncryptionKey = "TEAMS_ENCRYPTION_KEY"
	// MAIL_DEFAULT_LOCALE is the language outbound transactional mail falls back to
	// when the recipient's own preference is unknown. Must be a locale the mail
	// catalogue ships (internal/email); cmd/api rejects anything else at boot.
	envMailDefaultLocale = "MAIL_DEFAULT_LOCALE"
	// PROVISIONING_ENCRYPTION_KEY (hex 32 bytes) encrypts the per-org directory
	// client secret at rest. Its own key rather than the e-mail one: the two are
	// rotated by different decisions, and one key per purpose keeps a rotation from
	// taking out a capability nobody meant to touch.
	envProvisioningEncryptionKey = "PROVISIONING_ENCRYPTION_KEY"
	// CSC_ENCRYPTION_KEY (hex 32 bytes) encrypts the per-org CSC signing-provider
	// client secret at rest. Its own key rather than sharing another: the two are
	// rotated by different decisions, and one key per purpose keeps a rotation from
	// taking out a capability nobody meant to touch. Without it an org cannot store
	// a CSC client secret (internal/csc).
	envCSCEncryptionKey = "CSC_ENCRYPTION_KEY"
	// SIGNING_OAUTH_ISSUER_INTERNAL overrides the OAuth issuer base the backend uses
	// for its own server-side token exchange during the signing ceremony, when that
	// host differs from the browser-facing issuer. It exists for local Docker (the
	// authorization server is localhost:8084 to the browser but qtsp-authz:8084 to
	// the backend container). Empty in production, where the two are the same URL.
	envSigningOAuthIssuerInternal = "SIGNING_OAUTH_ISSUER_INTERNAL"
	// STATIC_DIR points at the built frontend; when set the API also serves it as
	// an SPA on "/". Unset in dev (Vite serves the frontend).
	envStaticDir = "STATIC_DIR"

	defaultAppBaseURL = "http://localhost:5173"
	defaultMailLocale = "en"

	// PostGuard: the internal sidecar that performs encrypt-and-upload, the shared
	// secret the backend presents to it, and the deployment master key that wraps
	// each org's own (owner-configured) encryption key at rest (envelope
	// encryption); the per-org key in turn encrypts that org's API key.
	envPostGuardSidecarURL    = "POSTGUARD_SIDECAR_URL"
	envPostGuardSharedSecret  = "POSTGUARD_SHARED_SECRET"
	envPostGuardEncryptionKey = "POSTGUARD_KEY_ENCRYPTION_KEY"
	// The three endpoints that together aim a deployment at one PostGuard
	// environment: the key service and the storage the sidecar uploads through,
	// and the public website the recipient download link points at (used only for
	// the "own SMTP" notification path). The backend consumes only the website;
	// it reads the other two to refuse a combination that mixes environments.
	envPostGuardPkgURL          = "POSTGUARD_PKG_URL"
	envPostGuardCryptifyURL     = "POSTGUARD_CRYPTIFY_URL"
	envPostGuardWebsiteURL      = "POSTGUARD_WEBSITE_URL"
	defaultPostGuardPkgURL      = "https://pkg.postguard.eu"
	defaultPostGuardCryptifyURL = "https://storage.postguard.eu"
	defaultPostGuardWebsiteURL  = "https://postguard.eu"

	// Host labels naming a PostGuard service within its environment, stripped to
	// compare the three URLs: pkg.staging.postguard.eu -> staging.postguard.eu.
	postGuardPkgLabel      = "pkg."
	postGuardCryptifyLabel = "storage."

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
	// AttestationHolderWSCAKEK is the hex-encoded 32-byte deployment key that
	// seals each org's WSCA activation secret at rest. Empty = WSCA not configured.
	AttestationHolderWSCAKEK string
	// AttestationHolderWSCAURL is the wallet-provider (WSCA) base URL. When set
	// (with the irmago holder), redemption binds holder keys via the WSCA.
	AttestationHolderWSCAURL string
	// AttestationHolderWSCAKeystoreDir is the parent dir for per-org walletmobile
	// keystores (a persistent volume).
	AttestationHolderWSCAKeystoreDir string
	// AttestationHolderWSCAInsecure trusts the wallet-provider's dev TLS cert.
	AttestationHolderWSCAInsecure bool

	AppBaseURL         string
	EmailEncryptionKey string
	// SlackEncryptionKey encrypts per-org Slack webhook URLs at rest. Empty means
	// the deployment cannot store one.
	SlackEncryptionKey string
	// TeamsEncryptionKey encrypts per-org Microsoft Teams webhook URLs at rest.
	// Empty means the deployment cannot store one.
	TeamsEncryptionKey string
	// ProvisioningEncryptionKey encrypts the per-org directory client secret at
	// rest. Empty means no organisation can store one, so directory provisioning
	// stays unavailable.
	ProvisioningEncryptionKey string
	// CSCEncryptionKey encrypts the per-org CSC signing-provider client secret at
	// rest. Empty means no organisation can store one.
	CSCEncryptionKey string
	// SigningOAuthIssuerInternal overrides the OAuth issuer base for the backend's
	// server-side token exchange during signing (local Docker only; empty in prod).
	SigningOAuthIssuerInternal string
	// MailDefaultLocale is the fallback language for outbound transactional mail.
	MailDefaultLocale string

	// StaticDir is the directory holding the built frontend (index.html + assets).
	// When set, the API server also serves it as an SPA on "/"; empty disables
	// static serving (dev serves the frontend via Vite).
	StaticDir string

	PostGuardSidecarURL    string
	PostGuardSharedSecret  string
	PostGuardEncryptionKey string
	// PostGuardPkgURL and PostGuardCryptifyURL are the key service and storage the
	// sidecar uploads through. The backend does not call them; it holds them so a
	// deployment that mixes PostGuard environments fails at startup.
	PostGuardPkgURL      string
	PostGuardCryptifyURL string
	// PostGuardWebsiteURL is the public base URL of the PostGuard website the
	// recipient download link points at (e.g. https://postguard.eu/download?uuid=…).
	// Used only for the "own SMTP" notification path, where the backend composes
	// the notification itself instead of letting PostGuard's service send it.
	PostGuardWebsiteURL string

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

	postguardURLs, err := loadPostGuardURLs()
	if err != nil {
		return Config{}, err
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

	// Outbound mail and QERDS messages build their links by concatenating onto
	// APP_BASE_URL, and internal/email refuses to render a link that is not an
	// absolute http(s) URL. Without this check a scheme-less value boots clean and
	// then fails every credential offer and invitation at send time.
	appBaseURL := envOrDefault(envAppBaseURL, defaultAppBaseURL)
	if err := requireAbsoluteHTTPURL(envAppBaseURL, appBaseURL); err != nil {
		return Config{}, err
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
		AttestationHolderWSCAKEK:         os.Getenv(envAttestationHolderWSCAKEK),
		AttestationHolderWSCAURL:         os.Getenv(envAttestationHolderWSCAURL),
		AttestationHolderWSCAKeystoreDir: os.Getenv(envAttestationHolderWSCAKeystoreDir),
		AttestationHolderWSCAInsecure: strings.EqualFold(
			os.Getenv(envAttestationHolderWSCAInsecure), "true"),

		AppBaseURL:                 appBaseURL,
		EmailEncryptionKey:         os.Getenv(envEmailEncryptionKey),
		SlackEncryptionKey:         os.Getenv(envSlackEncryptionKey),
		TeamsEncryptionKey:         os.Getenv(envTeamsEncryptionKey),
		ProvisioningEncryptionKey:  os.Getenv(envProvisioningEncryptionKey),
		CSCEncryptionKey:           os.Getenv(envCSCEncryptionKey),
		SigningOAuthIssuerInternal: os.Getenv(envSigningOAuthIssuerInternal),
		MailDefaultLocale:          envOrDefault(envMailDefaultLocale, defaultMailLocale),
		StaticDir:                  os.Getenv(envStaticDir),

		PostGuardSidecarURL:    os.Getenv(envPostGuardSidecarURL),
		PostGuardSharedSecret:  os.Getenv(envPostGuardSharedSecret),
		PostGuardEncryptionKey: os.Getenv(envPostGuardEncryptionKey),
		PostGuardPkgURL:        postguardURLs.pkg,
		PostGuardCryptifyURL:   postguardURLs.cryptify,
		PostGuardWebsiteURL:    postguardURLs.website,

		PlatformAdminEmails: parseList(os.Getenv(envPlatformAdminEmails)),
	}, nil
}

// postGuardURLs holds the three endpoints that aim a deployment at one PostGuard
// environment.
type postGuardURLs struct {
	pkg      string
	cryptify string
	website  string
}

// loadPostGuardURLs reads the three PostGuard endpoints and refuses a combination
// that mixes environments. "Upload to staging, link to production" is otherwise
// silent: the notification is delivered and only the recipient finds out the
// download link is dead.
func loadPostGuardURLs() (postGuardURLs, error) {
	urls := postGuardURLs{
		pkg:      envOrDefault(envPostGuardPkgURL, defaultPostGuardPkgURL),
		cryptify: envOrDefault(envPostGuardCryptifyURL, defaultPostGuardCryptifyURL),
		website:  envOrDefault(envPostGuardWebsiteURL, defaultPostGuardWebsiteURL),
	}

	pkgEnv, err := postGuardEnvironment(envPostGuardPkgURL, urls.pkg, postGuardPkgLabel)
	if err != nil {
		return postGuardURLs{}, err
	}
	cryptifyEnv, err := postGuardEnvironment(envPostGuardCryptifyURL, urls.cryptify, postGuardCryptifyLabel)
	if err != nil {
		return postGuardURLs{}, err
	}
	websiteEnv, err := postGuardEnvironment(envPostGuardWebsiteURL, urls.website, "")
	if err != nil {
		return postGuardURLs{}, err
	}

	if pkgEnv != websiteEnv || cryptifyEnv != websiteEnv {
		return postGuardURLs{}, fmt.Errorf(
			"config: PostGuard URLs name different environments (%s=%q, %s=%q, %s=%q); switch all three together",
			envPostGuardPkgURL, urls.pkg,
			envPostGuardCryptifyURL, urls.cryptify,
			envPostGuardWebsiteURL, urls.website)
	}
	return urls, nil
}

// postGuardEnvironment validates one PostGuard URL as an absolute http(s) URL and
// reduces it to the environment host the three are compared on. serviceLabel is
// the host label naming the service within its environment and is stripped when
// present; the website carries no such label, so it passes an empty one.
func postGuardEnvironment(key, raw, serviceLabel string) (string, error) {
	if err := requireAbsoluteHTTPURL(key, raw); err != nil {
		return "", err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("config: %s %q: %w", key, raw, err)
	}
	host := strings.ToLower(u.Hostname())
	if serviceLabel != "" {
		host = strings.TrimPrefix(host, serviceLabel)
	}
	return host, nil
}

// requireAbsoluteHTTPURL rejects a configured URL that cannot be used as a link
// base: a relative path, a missing or non-http(s) scheme, or a missing host. Shared
// so every URL variable reports the same requirement in the same words.
func requireAbsoluteHTTPURL(key, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("config: %s %q: %w", key, raw, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("config: %s %q must be an absolute http(s) URL", key, raw)
	}
	return nil
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
