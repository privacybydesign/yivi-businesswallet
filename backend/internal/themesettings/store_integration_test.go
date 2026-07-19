//go:build integration

package themesettings_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/themesettings"
)

func TestGetSettingsUnconfigured(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := themesettings.NewStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")

	got, err := store.GetSettings(context.Background(), orgID)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.Configured {
		t.Errorf("Configured = true, want false for an org with no theme")
	}
}

func TestUpsertThenGetRoundtrips(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := themesettings.NewStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	in := themesettings.SettingsInput{
		PrimaryColor: "#1d4e89",
		AccentColor:  "#ba3354",
		LogoURI:      "https://example.com/logo.svg",
	}
	saved, err := store.Upsert(ctx, orgID, in)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if !saved.Configured || saved.PrimaryColor != in.PrimaryColor ||
		saved.AccentColor != in.AccentColor || saved.LogoURI != in.LogoURI {
		t.Fatalf("Upsert returned %+v, want the saved input", saved)
	}
	if saved.UpdatedAt == nil {
		t.Error("UpdatedAt is nil after Upsert")
	}

	got, err := store.GetSettings(ctx, orgID)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	// Settings holds a *time.Time, so compare fields rather than the struct
	// (pointer identity would differ even when the values match).
	if got.Configured != saved.Configured || got.PrimaryColor != saved.PrimaryColor ||
		got.AccentColor != saved.AccentColor || got.LogoURI != saved.LogoURI ||
		got.UpdatedAt == nil || !got.UpdatedAt.Equal(*saved.UpdatedAt) {
		t.Errorf("GetSettings = %+v, want %+v", got, saved)
	}
}

func TestUpsertOverwritesExistingRow(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := themesettings.NewStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	if _, err := store.Upsert(ctx, orgID, themesettings.SettingsInput{PrimaryColor: "#111111"}); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}
	// A second upsert must update in place (organization_id is UNIQUE), clearing
	// the logo back to the default look.
	updated, err := store.Upsert(ctx, orgID, themesettings.SettingsInput{PrimaryColor: "#222222"})
	if err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	if updated.PrimaryColor != "#222222" || updated.LogoURI != "" {
		t.Errorf("after overwrite = %+v, want primary #222222 and empty logo", updated)
	}
}

// failingRecorder makes the in-transaction audit write fail, so the upsert must
// roll back and leave no row behind.
type failingRecorder struct{}

func (failingRecorder) Record(context.Context, database.Querier, string, audit.Target, map[string]any) error {
	return errors.New("audit boom")
}

func TestUpsertRollsBackWhenAuditFails(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := themesettings.NewStore(pool, failingRecorder{})
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	if _, err := store.Upsert(ctx, orgID, themesettings.SettingsInput{PrimaryColor: "#333333"}); err == nil {
		t.Fatal("Upsert succeeded, want audit failure")
	}

	got, err := themesettings.NewStore(pool, audit.NopRecorder{}).GetSettings(ctx, orgID)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.Configured {
		t.Error("theme row persisted despite the audit failure")
	}
}

func makeOrg(t *testing.T, pool *pgxpool.Pool, slug string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		slug, slug, "kvk-"+slug, "NL.KVK."+slug, slug+"@qerds.localhost").Scan(&id)
	if err != nil {
		t.Fatalf("create org %q: %v", slug, err)
	}
	return id
}
