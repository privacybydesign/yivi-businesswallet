//go:build integration

package migrate_test

import (
	"context"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/migrate"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// An API key created without scopes may do everything.
func TestAPIKeyScopesDefaultToEvery(t *testing.T) {
	ctx := context.Background()
	pool, _ := testdb.Fresh(t)

	var orgID string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('Scope Test BV', 'scope-test', '12345678', 'NLNHR.12345678', 'scope-test@qerds.test') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	var scopes []string
	if err := pool.QueryRow(ctx, `INSERT INTO identity_proofing_api_keys (organization_id, name, prefix, secret_hash)
		VALUES ($1, 'yp_live_key', 'yp_live_key', 'secret') RETURNING scopes`, orgID).Scan(&scopes); err != nil {
		t.Fatalf("insert key: %v", err)
	}

	for _, want := range []string{
		"sessions:write", "sessions:read", "results:read", "flows:read",
		"flows:write", "settings:read", "settings:write", "webhooks:write",
		"reviews:read", "reviews:write",
	} {
		if !slices.Contains(scopes, want) {
			t.Errorf("scopes = %v, missing %s", scopes, want)
		}
	}
}

// adminScopesVersion is 20261116090100_add_identity_proofing_api_key_admin_scopes.sql.
const adminScopesVersion int64 = 20261116090100

// A key made before the setup scopes existed gets them, once each.
func TestAPIKeyScopesUpgradeExistingKeys(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.Bare(t)
	if err := migrate.UpTo(ctx, dsn, adminScopesVersion-1); err != nil {
		t.Fatalf("migrate up to before %d: %v", adminScopesVersion, err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var orgID, keyID string
	if err := conn.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('Scope Test BV', 'scope-test', '12345678', 'NLNHR.12345678', 'scope-test@qerds.test') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO identity_proofing_api_keys (organization_id, name, prefix, secret_hash, scopes)
		VALUES ($1, 'old', 'yp_live_old', 'secret', ARRAY['sessions:write', 'sessions:read', 'results:read', 'flows:read', 'flows:write'])
		RETURNING id`, orgID).Scan(&keyID); err != nil {
		t.Fatalf("insert key: %v", err)
	}

	if err := migrate.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	var scopes []string
	if err := conn.QueryRow(ctx, `SELECT scopes FROM identity_proofing_api_keys WHERE id = $1`, keyID).Scan(&scopes); err != nil {
		t.Fatalf("read key: %v", err)
	}
	want := []string{
		"sessions:write", "sessions:read", "results:read", "flows:read", "flows:write",
		"settings:read", "settings:write", "webhooks:write",
	}
	if !slices.Equal(scopes, want) {
		t.Errorf("scopes = %v, want %v", scopes, want)
	}
}
