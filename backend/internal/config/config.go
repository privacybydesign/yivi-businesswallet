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

	envPlatformAdminEmails = "PLATFORM_ADMIN_EMAILS"

	envQerdsProvider             = "QERDS_PROVIDER"
	envQerdsProviderURL          = "QERDS_PROVIDER_URL"
	envQerdsAuthToken            = "QERDS_AUTH_TOKEN"
	envQerdsWebhookSecret        = "QERDS_WEBHOOK_SECRET"
	envQerdsDefaultAddressDomain = "QERDS_DEFAULT_ADDRESS_DOMAIN"

	envWalletRegistryProvider = "WALLET_REGISTRY_PROVIDER"

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

	// ProviderStub selects the in-process StubProvider (local dev / CI).
	ProviderStub = "stub"
	// ProviderDomibus selects the Domibus AS4 access-point driver. Requires
	// QERDS_PROVIDER_URL (the WS-plugin endpoint).
	ProviderDomibus = "domibus"

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

	qerdsProvider := envOrDefault(envQerdsProvider, defaultQerdsProvider)
	qerdsProviderURL := os.Getenv(envQerdsProviderURL)
	if qerdsProvider != ProviderStub && qerdsProviderURL == "" {
		return Config{}, fmt.Errorf("config: %s must be set when %s is not %q", envQerdsProviderURL, envQerdsProvider, ProviderStub)
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
