//go:build integration

package csc

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

const testKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func newTestStore(t *testing.T, builtIn *BuiltIn) (*Store, *pgxpool.Pool) {
	t.Helper()
	pool, _ := testdb.Fresh(t)
	cipher, err := crypto.NewCipher(testKey)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	return NewStore(pool, audit.NewDBRecorder(), cipher, builtIn), pool
}

func makeOrg(t *testing.T, pool *pgxpool.Pool, slug string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		"Acme", slug, slug+"-kvk", slug+"-euid", slug+"-address").Scan(&id)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return id
}

// An organisation that never opened the settings signs with the wallet's own
// service: that is what "preconfigured for every organisation" comes down to.
func TestAnOrgThatSavedNothingUsesTheBuiltInService(t *testing.T) {
	store, pool := newTestStore(t, testBuiltIn)
	ctx := context.Background()
	orgID := makeOrg(t, pool, "acme")

	settings, err := store.GetSettings(ctx, orgID)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if !settings.Configured || !settings.Enabled || settings.ProviderKind != ProviderKindYivi || settings.BaseURL != testBuiltIn.BaseURL {
		t.Errorf("settings = %+v, want the built-in service, configured and enabled", settings)
	}
	if available, err := store.Available(ctx, orgID); err != nil || !available {
		t.Errorf("Available = %v, %v; want signing available with nothing saved", available, err)
	}
	baseURL, clientID, secret, err := store.ResolveConnection(ctx, orgID)
	if err != nil || baseURL != testBuiltIn.BaseURL || clientID != testBuiltIn.ClientID || secret != testBuiltIn.ClientSecret {
		t.Errorf("connection = %q %q %q, %v; want the built-in service's", baseURL, clientID, secret, err)
	}
}

// Where the deployment does not run the service, nothing changes from before: an
// org that saved nothing has no provider.
func TestWithoutTheBuiltInServiceAnOrgHasNoProviderUntilItConnectsOne(t *testing.T) {
	store, pool := newTestStore(t, nil)
	ctx := context.Background()
	orgID := makeOrg(t, pool, "acme")

	settings, err := store.GetSettings(ctx, orgID)
	if err != nil || settings.Configured || settings.Enabled {
		t.Fatalf("settings = %+v, %v; want nothing configured", settings, err)
	}
	if available, _ := store.Available(ctx, orgID); available {
		t.Error("signing must not be available with no provider at all")
	}
	if baseURL, _, _, err := store.ResolveConnection(ctx, orgID); err != nil || baseURL != "" {
		t.Errorf("connection base URL = %q, %v; want none", baseURL, err)
	}
	if _, err := store.BaseURLFor(ctx, orgID); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("BaseURLFor = %v, want ErrNotConfigured", err)
	}
	in, err := Normalize(SettingsInput{Enabled: true, ProviderKind: ProviderKindYivi})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if _, err := store.Upsert(ctx, orgID, in); !errors.Is(err, ErrBuiltInUnavailable) {
		t.Errorf("choosing the built-in service where it does not run = %v, want ErrBuiltInUnavailable", err)
	}
}

// An admin can switch the built-in service off, connect another provider, and
// come back: each state is what the ceremony then gets.
func TestSwitchingBetweenTheBuiltInServiceAndAnotherProvider(t *testing.T) {
	store, pool := newTestStore(t, testBuiltIn)
	ctx := context.Background()
	orgID := makeOrg(t, pool, "acme")
	save := func(in SettingsInput) Settings {
		t.Helper()
		normalized, err := Normalize(in)
		if err != nil {
			t.Fatalf("Normalize: %v", err)
		}
		settings, err := store.Upsert(ctx, orgID, normalized)
		if err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		return settings
	}

	// Off: the org does not sign.
	off := save(SettingsInput{Enabled: false, ProviderKind: ProviderKindYivi})
	if off.Enabled || off.ProviderKind != ProviderKindYivi {
		t.Errorf("settings after switching off = %+v", off)
	}
	if available, _ := store.Available(ctx, orgID); available {
		t.Error("signing must not be available once the admin switched it off")
	}
	if baseURL, _, _, _ := store.ResolveConnection(ctx, orgID); baseURL != "" {
		t.Errorf("a switched-off built-in service still resolved to %q", baseURL)
	}

	// Another provider, with its own secret.
	secret := "remote-secret"
	custom := save(SettingsInput{
		Enabled: true, ProviderKind: ProviderKindCustom,
		BaseURL: "https://qtsp.example/", ClientID: "acme", ClientSecret: &secret,
	})
	if custom.ProviderKind != ProviderKindCustom || custom.BaseURL != "https://qtsp.example" || !custom.HasClientSecret {
		t.Errorf("custom settings = %+v", custom)
	}
	baseURL, clientID, got, err := store.ResolveConnection(ctx, orgID)
	if err != nil || baseURL != "https://qtsp.example" || clientID != "acme" || got != secret {
		t.Errorf("custom connection = %q %q %q, %v", baseURL, clientID, got, err)
	}

	// Back to the built-in service: the remote endpoint and its secret are gone.
	back := save(SettingsInput{Enabled: true, ProviderKind: ProviderKindYivi, BaseURL: "https://qtsp.example", ClientID: "acme"})
	if !back.Enabled || back.ProviderKind != ProviderKindYivi || back.BaseURL != testBuiltIn.BaseURL || back.ClientID != "" || back.HasClientSecret {
		t.Errorf("settings after coming back = %+v, want the built-in service and nothing of the remote one", back)
	}
	baseURL, clientID, got, err = store.ResolveConnection(ctx, orgID)
	if err != nil || baseURL != testBuiltIn.BaseURL || clientID != testBuiltIn.ClientID || got != testBuiltIn.ClientSecret {
		t.Errorf("connection after coming back = %q %q %q, %v", baseURL, clientID, got, err)
	}

	// The first save is audited as a change from the default the org had.
	var before string
	err = pool.QueryRow(ctx, `SELECT metadata->'before'->>'providerKind' FROM audit_events
		WHERE organization_id = $1 AND action = $2 ORDER BY occurred_at LIMIT 1`, orgID, string(audit.CSCSettingsUpdated)).Scan(&before)
	if err != nil || before != string(ProviderKindYivi) {
		t.Errorf("first audited before.providerKind = %q, %v; want the built-in default", before, err)
	}
}
