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

	envIrmaRequestorURL    = "IRMA_REQUESTOR_URL"
	envIrmaRequestorToken  = "IRMA_REQUESTOR_TOKEN"
	envIrmaClientURL       = "IRMA_CLIENT_URL"
	envIrmaEmailAttribute  = "IRMA_EMAIL_ATTRIBUTE"
	envSessionCookieSecure = "SESSION_COOKIE_SECURE"
	envSessionTTL          = "SESSION_TTL"
	envSessionPruneEvery   = "SESSION_PRUNE_INTERVAL"

	envPlatformAdminEmails = "PLATFORM_ADMIN_EMAILS"

	defaultLogLevel  = "info"
	defaultLogFormat = "text"
	defaultLogSource = "true"

	defaultIrmaRequestorURL    = "http://irma:8088"
	defaultIrmaRequestorToken  = ""
	defaultIrmaClientURL       = ""
	defaultIrmaEmailAttribute  = "irma-demo.sidn-pbdf.email.email"
	defaultSessionCookieSecure = "false"
	defaultSessionTTL          = "24h"
	defaultSessionPruneEvery   = "1h"
)

type Config struct {
	DatabaseDSN string
	LogLevel    string
	LogFormat   string
	LogSource   bool

	IrmaRequestorURL    string
	IrmaRequestorToken  string
	IrmaClientURL       string
	IrmaEmailAttribute  string
	SessionCookieSecure bool
	SessionTTL          time.Duration
	SessionPruneEvery   time.Duration

	PlatformAdminEmails []string
}

func Load() (Config, error) {
	dsn := os.Getenv(envDatabaseURL)
	if dsn == "" {
		return Config{}, fmt.Errorf("%s is required", envDatabaseURL)
	}

	cookieSecure := strings.EqualFold(envOrDefault(envSessionCookieSecure, defaultSessionCookieSecure), "true")

	requestorURL := envOrDefault(envIrmaRequestorURL, defaultIrmaRequestorURL)
	if cookieSecure && os.Getenv(envIrmaRequestorURL) == "" {
		return Config{}, fmt.Errorf("config: %s must be set when %s is true", envIrmaRequestorURL, envSessionCookieSecure)
	}

	sessionTTL, err := parseDuration(envSessionTTL, defaultSessionTTL)
	if err != nil {
		return Config{}, err
	}

	sessionPruneEvery, err := parseDuration(envSessionPruneEvery, defaultSessionPruneEvery)
	if err != nil {
		return Config{}, err
	}

	return Config{
		DatabaseDSN: dsn,
		LogLevel:    envOrDefault(envLogLevel, defaultLogLevel),
		LogFormat:   envOrDefault(envLogFormat, defaultLogFormat),
		LogSource:   strings.EqualFold(envOrDefault(envLogSource, defaultLogSource), "true"),

		IrmaRequestorURL:    requestorURL,
		IrmaRequestorToken:  envOrDefault(envIrmaRequestorToken, defaultIrmaRequestorToken),
		IrmaClientURL:       envOrDefault(envIrmaClientURL, defaultIrmaClientURL),
		IrmaEmailAttribute:  envOrDefault(envIrmaEmailAttribute, defaultIrmaEmailAttribute),
		SessionCookieSecure: cookieSecure,
		SessionTTL:          sessionTTL,
		SessionPruneEvery:   sessionPruneEvery,

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
