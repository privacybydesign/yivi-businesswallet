//go:build integration

package provisioning_test

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/provisioner"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/provisioning"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func newStore(t *testing.T, pool *pgxpool.Pool) *provisioning.Store {
	t.Helper()
	cipher, err := crypto.NewCipher(hex.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	return provisioning.NewStore(pool, audit.NopRecorder{}, cipher)
}

func input() provisioning.SettingsInput {
	secret := "s3cret"
	return provisioning.SettingsInput{
		Enabled:       true,
		Source:        provisioner.SourceEntra,
		TenantID:      "tenant",
		ClientID:      "client",
		ClientSecret:  &secret,
		GroupID:       "staff",
		AdminGroupIDs: []string{"admins"},
	}
}

func TestGetSettingsUnconfigured(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	orgID := makeOrg(t, pool, "acme")

	got, err := newStore(t, pool).GetSettings(context.Background(), orgID)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.Configured {
		t.Error("Configured = true, want false for an org that never saved a configuration")
	}
	if got.Source != provisioner.SourceEntra {
		t.Errorf("Source = %q, want the default source offered to a first-time configurer", got.Source)
	}
}

func TestSaveRoundtripsWithoutEverServingTheSecret(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := newStore(t, pool)
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	saved, err := store.Save(ctx, orgID, input())
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !saved.Configured || !saved.HasClientSecret || saved.UpdatedAt == nil {
		t.Fatalf("Save returned %+v, want a configured row with a stored secret", saved)
	}

	got, err := store.GetSettings(ctx, orgID)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.TenantID != "tenant" || got.GroupID != "staff" || len(got.AdminGroupIDs) != 1 {
		t.Errorf("settings = %+v, want the saved configuration", got)
	}

	// The secret comes back only through the sync's own resolver, and the stored
	// column must be ciphertext.
	source, cfg, err := store.SourceConfig(ctx, orgID)
	if err != nil {
		t.Fatalf("SourceConfig: %v", err)
	}
	if source != provisioner.SourceEntra || cfg.ClientSecret != "s3cret" {
		t.Errorf("source config = %q/%+v, want the decrypted Entra credentials", source, cfg)
	}
	var stored []byte
	if err := pool.QueryRow(ctx,
		"SELECT client_secret_ciphertext FROM org_provisioning_settings WHERE organization_id = $1",
		orgID).Scan(&stored); err != nil {
		t.Fatalf("read ciphertext: %v", err)
	}
	if strings.Contains(string(stored), "s3cret") {
		t.Error("the client secret is stored in the clear")
	}
}

func TestSaveWithoutASecretKeepsTheStoredOne(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := newStore(t, pool)
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	if _, err := store.Save(ctx, orgID, input()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The settings screen never holds the secret, so a save that only changes the
	// group must not wipe it.
	second := input()
	second.ClientSecret = nil
	second.GroupID = "everyone"
	if _, err := store.Save(ctx, orgID, second); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, cfg, err := store.SourceConfig(ctx, orgID)
	if err != nil {
		t.Fatalf("SourceConfig: %v", err)
	}
	if cfg.ClientSecret != "s3cret" || cfg.GroupID != "everyone" {
		t.Errorf("config = %+v, want the kept secret and the new group", cfg)
	}
}

func TestSaveWithAnEmptySecretClearsIt(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := newStore(t, pool)
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	if _, err := store.Save(ctx, orgID, input()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cleared := input()
	empty := ""
	cleared.ClientSecret = &empty
	got, err := store.Save(ctx, orgID, cleared)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got.HasClientSecret {
		t.Error("HasClientSecret = true after clearing the secret")
	}
}

func TestSourceConfigDistinguishesUnconfiguredFromDisabled(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := newStore(t, pool)
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	if _, _, err := store.SourceConfig(ctx, orgID); !errors.Is(err, provisioning.ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}

	off := input()
	off.Enabled = false
	if _, err := store.Save(ctx, orgID, off); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, _, err := store.SourceConfig(ctx, orgID); !errors.Is(err, provisioning.ErrDisabled) {
		t.Fatalf("err = %v, want ErrDisabled", err)
	}
}

func TestListEnabledReturnsOnlyTheOrganisationsToSync(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := newStore(t, pool)
	ctx := context.Background()

	on := makeOrg(t, pool, "acme")
	off := makeOrg(t, pool, "beta")
	if _, err := store.Save(ctx, on, input()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	disabled := input()
	disabled.Enabled = false
	if _, err := store.Save(ctx, off, disabled); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.ListEnabled(ctx)
	if err != nil {
		t.Fatalf("ListEnabled: %v", err)
	}
	if len(got) != 1 || got[0] != on {
		t.Errorf("ListEnabled = %v, want only %s", got, on)
	}
}

func TestRecordRunStoresTheOutcome(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := newStore(t, pool)
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()
	if _, err := store.Save(ctx, orgID, input()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	runErr := errors.New(strings.Repeat("e", 2000))
	if err := store.RecordRun(ctx, orgID, provisioner.SourceEntra, provisioning.Result{}, runErr); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}
	got, err := store.GetSettings(ctx, orgID)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.LastRunStatus != provisioning.RunFailed || got.LastRunAt == nil {
		t.Fatalf("settings = %+v, want a recorded failure", got)
	}
	// A verbose source must not be able to grow the settings row without limit.
	if len(got.LastRunError) >= 2000 {
		t.Errorf("lastRunError is %d bytes, want it bounded", len(got.LastRunError))
	}

	if err := store.RecordRun(ctx, orgID, provisioner.SourceEntra,
		provisioning.Result{MembersInvited: 3}, nil); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}
	if got, err = store.GetSettings(ctx, orgID); err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.LastRunStatus != provisioning.RunSucceeded || got.LastRunError != "" {
		t.Errorf("settings = %+v, want the earlier failure cleared", got)
	}
}

func TestMemberLinksRoundtrip(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := newStore(t, pool)
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	if err := store.LinkMember(ctx, orgID, provisioner.SourceEntra, "u1", "ada@example.org"); err != nil {
		t.Fatalf("LinkMember: %v", err)
	}
	// Re-linking the same person is how a repeat sync behaves; it must update
	// rather than fail on the primary key.
	if err := store.LinkMember(ctx, orgID, provisioner.SourceEntra, "u1", "ada@example.org"); err != nil {
		t.Fatalf("LinkMember again: %v", err)
	}

	links, err := store.MemberLinks(ctx, orgID, provisioner.SourceEntra)
	if err != nil {
		t.Fatalf("MemberLinks: %v", err)
	}
	if len(links) != 1 || links["u1"].Email != "ada@example.org" {
		t.Fatalf("links = %v, want the one link", links)
	}

	if err := store.UnlinkMember(ctx, orgID, provisioner.SourceEntra, "u1"); err != nil {
		t.Fatalf("UnlinkMember: %v", err)
	}
	if links, err = store.MemberLinks(ctx, orgID, provisioner.SourceEntra); err != nil {
		t.Fatalf("MemberLinks: %v", err)
	}
	if len(links) != 0 {
		t.Errorf("links = %v, want none after unlinking", links)
	}
}

func TestDepartmentLinkDropsWithItsDepartment(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := newStore(t, pool)
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	var deptID uuid.UUID
	if err := pool.QueryRow(ctx,
		"INSERT INTO departments (organization_id, name) VALUES ($1, $2) RETURNING id",
		orgID, "Research").Scan(&deptID); err != nil {
		t.Fatalf("create department: %v", err)
	}
	if err := store.LinkDepartment(ctx, orgID, provisioner.SourceEntra, "research", deptID); err != nil {
		t.Fatalf("LinkDepartment: %v", err)
	}

	// An admin deleting the department must not leave a link pointing at nothing —
	// the next sync would hand that id to an invitation and hit the foreign key.
	if _, err := pool.Exec(ctx, "DELETE FROM departments WHERE id = $1", deptID); err != nil {
		t.Fatalf("delete department: %v", err)
	}
	links, err := store.DepartmentLinks(ctx, orgID, provisioner.SourceEntra)
	if err != nil {
		t.Fatalf("DepartmentLinks: %v", err)
	}
	if len(links) != 0 {
		t.Errorf("links = %v, want the link gone with its department", links)
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
