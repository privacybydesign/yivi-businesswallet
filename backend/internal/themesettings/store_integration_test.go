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

func TestSaveThenGetRoundtripsColours(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := themesettings.NewStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	in := themesettings.SettingsInput{PrimaryColor: "#1d4e89", AccentColor: "#ba3354"}
	saved, err := store.Save(ctx, orgID, in, themesettings.LogoUpdate{})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !saved.Configured || saved.PrimaryColor != in.PrimaryColor ||
		saved.AccentColor != in.AccentColor || saved.HasLogo {
		t.Fatalf("Save returned %+v, want the saved colours and no logo", saved)
	}
	if saved.UpdatedAt == nil {
		t.Error("UpdatedAt is nil after Save")
	}

	got, err := store.GetSettings(ctx, orgID)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	// Settings holds a *time.Time, so compare fields rather than the struct
	// (pointer identity would differ even when the values match).
	if got.Configured != saved.Configured || got.PrimaryColor != saved.PrimaryColor ||
		got.AccentColor != saved.AccentColor || got.HasLogo != saved.HasLogo ||
		got.UpdatedAt == nil || !got.UpdatedAt.Equal(*saved.UpdatedAt) {
		t.Errorf("GetSettings = %+v, want %+v", got, saved)
	}
}

func TestSaveKeepsLogoWhenNotReplaced(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := themesettings.NewStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	logo := themesettings.Logo{Bytes: []byte("\x89PNG\r\n\x1a\n data"), ContentType: "image/png"}
	if _, err := store.Save(ctx, orgID, themesettings.SettingsInput{PrimaryColor: "#111111"},
		themesettings.LogoUpdate{Replace: true, Logo: logo}); err != nil {
		t.Fatalf("first Save (with logo): %v", err)
	}

	// A colour-only save (Replace false) must not disturb the stored logo.
	updated, err := store.Save(ctx, orgID, themesettings.SettingsInput{PrimaryColor: "#222222"},
		themesettings.LogoUpdate{})
	if err != nil {
		t.Fatalf("second Save (colours only): %v", err)
	}
	if updated.PrimaryColor != "#222222" || !updated.HasLogo {
		t.Fatalf("after colour-only save = %+v, want primary #222222 and the logo kept", updated)
	}

	got, err := store.GetLogo(ctx, orgID)
	if err != nil {
		t.Fatalf("GetLogo: %v", err)
	}
	if got.ContentType != logo.ContentType || string(got.Bytes) != string(logo.Bytes) {
		t.Errorf("GetLogo = %+v, want the originally uploaded logo", got)
	}
}

func TestSaveReplacesAndClearsLogo(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := themesettings.NewStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	logo := themesettings.Logo{Bytes: []byte("GIF89a data"), ContentType: "image/gif"}
	stored, err := store.Save(ctx, orgID, themesettings.SettingsInput{},
		themesettings.LogoUpdate{Replace: true, Logo: logo})
	if err != nil {
		t.Fatalf("Save (store logo): %v", err)
	}
	if !stored.HasLogo {
		t.Fatalf("HasLogo = false after storing a logo")
	}

	// Replace with an empty logo clears it.
	cleared, err := store.Save(ctx, orgID, themesettings.SettingsInput{},
		themesettings.LogoUpdate{Replace: true})
	if err != nil {
		t.Fatalf("Save (clear logo): %v", err)
	}
	if cleared.HasLogo {
		t.Errorf("HasLogo = true after clearing the logo")
	}
	if _, err := store.GetLogo(ctx, orgID); !errors.Is(err, themesettings.ErrNoLogo) {
		t.Errorf("GetLogo after clear = %v, want ErrNoLogo", err)
	}
}

func TestGetLogoUnsetReturnsErrNoLogo(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := themesettings.NewStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	// No row at all.
	if _, err := store.GetLogo(ctx, orgID); !errors.Is(err, themesettings.ErrNoLogo) {
		t.Errorf("GetLogo with no row = %v, want ErrNoLogo", err)
	}

	// Row exists (colours saved) but no logo bytes.
	if _, err := store.Save(ctx, orgID, themesettings.SettingsInput{PrimaryColor: "#333333"},
		themesettings.LogoUpdate{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := store.GetLogo(ctx, orgID); !errors.Is(err, themesettings.ErrNoLogo) {
		t.Errorf("GetLogo with colours but no logo = %v, want ErrNoLogo", err)
	}
}

// failingRecorder makes the in-transaction audit write fail, so the save must
// roll back and leave no row behind.
type failingRecorder struct{}

func (failingRecorder) Record(context.Context, database.Querier, string, audit.Target, map[string]any) error {
	return errors.New("audit boom")
}

func TestSaveRollsBackWhenAuditFails(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := themesettings.NewStore(pool, failingRecorder{})
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	if _, err := store.Save(ctx, orgID, themesettings.SettingsInput{PrimaryColor: "#333333"},
		themesettings.LogoUpdate{}); err == nil {
		t.Fatal("Save succeeded, want audit failure")
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
