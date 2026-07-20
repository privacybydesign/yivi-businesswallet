package config

import (
	"testing"
	"time"
)

// setValidQerdsEnv sets a minimal env that satisfies Load: a database DSN plus a
// real QERDS provider connection and a webhook secret for the default push mode.
// Each field is set via t.Setenv so it is restored after the test.
func setValidQerdsEnv(t *testing.T) {
	t.Helper()
	t.Setenv(envDatabaseURL, "postgres://localhost/test")
	t.Setenv(envQerdsProvider, ProviderDomibus)
	t.Setenv(envQerdsProviderURL, "http://domibus:8080/domibus/services/backend")
	t.Setenv(envQerdsWebhookSecret, "webhook-secret")
	// Explicitly clear the optional inbound-mode knobs so a value in the ambient
	// environment can't leak into a test that means to exercise the default.
	t.Setenv(envQerdsInboundMode, "")
	t.Setenv(envQerdsPollInterval, "")
}

func TestLoadRequiresQerdsProvider(t *testing.T) {
	setValidQerdsEnv(t)
	t.Setenv(envQerdsProvider, "")

	if _, err := Load(); err == nil {
		t.Fatal("expected an error when QERDS_PROVIDER is unset, got nil")
	}
}

func TestLoadRequiresQerdsProviderURL(t *testing.T) {
	setValidQerdsEnv(t)
	t.Setenv(envQerdsProviderURL, "")

	if _, err := Load(); err == nil {
		t.Fatal("expected an error when QERDS_PROVIDER_URL is unset, got nil")
	}
}

func TestLoadDefaultsToPushInboundMode(t *testing.T) {
	setValidQerdsEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.QerdsInboundMode != QerdsInboundPush {
		t.Fatalf("inbound mode = %q, want %q", cfg.QerdsInboundMode, QerdsInboundPush)
	}
	if cfg.QerdsProvider != ProviderDomibus {
		t.Fatalf("provider = %q, want %q", cfg.QerdsProvider, ProviderDomibus)
	}
	if cfg.QerdsPollInterval <= 0 {
		t.Fatalf("poll interval = %v, want a positive default", cfg.QerdsPollInterval)
	}
	// The wallet-registry stub is a separate subsystem and must keep its default.
	if cfg.WalletRegistryProvider != WalletRegistryStub {
		t.Fatalf("wallet registry provider = %q, want %q", cfg.WalletRegistryProvider, WalletRegistryStub)
	}
}

func TestLoadPushModeRequiresWebhookSecret(t *testing.T) {
	setValidQerdsEnv(t)
	t.Setenv(envQerdsInboundMode, QerdsInboundPush)
	t.Setenv(envQerdsWebhookSecret, "")

	if _, err := Load(); err == nil {
		t.Fatal("expected an error when push mode has no webhook secret, got nil")
	}
}

func TestLoadBothModeRequiresWebhookSecret(t *testing.T) {
	setValidQerdsEnv(t)
	t.Setenv(envQerdsInboundMode, QerdsInboundBoth)
	t.Setenv(envQerdsWebhookSecret, "")

	if _, err := Load(); err == nil {
		t.Fatal("expected an error when both mode has no webhook secret, got nil")
	}
}

func TestLoadPollModeNeedsNoWebhookSecret(t *testing.T) {
	setValidQerdsEnv(t)
	t.Setenv(envQerdsInboundMode, QerdsInboundPoll)
	t.Setenv(envQerdsWebhookSecret, "")
	t.Setenv(envQerdsPollInterval, "30s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.QerdsInboundMode != QerdsInboundPoll {
		t.Fatalf("inbound mode = %q, want %q", cfg.QerdsInboundMode, QerdsInboundPoll)
	}
	if cfg.QerdsPollInterval != 30*time.Second {
		t.Fatalf("poll interval = %v, want 30s", cfg.QerdsPollInterval)
	}
}

func TestLoadRejectsUnknownInboundMode(t *testing.T) {
	setValidQerdsEnv(t)
	t.Setenv(envQerdsInboundMode, "carrier-pigeon")

	if _, err := Load(); err == nil {
		t.Fatal("expected an error for an unknown inbound mode, got nil")
	}
}

func TestLoadRejectsNonPositivePollInterval(t *testing.T) {
	setValidQerdsEnv(t)
	t.Setenv(envQerdsInboundMode, QerdsInboundPoll)
	t.Setenv(envQerdsPollInterval, "0s")

	if _, err := Load(); err == nil {
		t.Fatal("expected an error for a non-positive poll interval, got nil")
	}
}
