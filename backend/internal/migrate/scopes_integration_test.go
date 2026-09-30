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

// apiKeyScopesVersion is 20260929140000_add_identity_proofing_api_key_scopes.sql.
const apiKeyScopesVersion int64 = 20260929140000

// A key from before scopes keeps everything it could do, and a key added
// afterwards without scopes gets the same.
func TestAPIKeyScopesKeepExistingKeysWhole(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.Bare(t)
	if err := migrate.UpTo(ctx, dsn, apiKeyScopesVersion-1); err != nil {
		t.Fatalf("migrate up to before %d: %v", apiKeyScopesVersion, err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var orgID, customerID string
	if err := conn.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('Scope Test BV', 'scope-test', '12345678', 'NLNHR.12345678', 'scope-test@qerds.test') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO identity_proofing_customers (organization_id, name)
		VALUES ($1, 'Initech') RETURNING id`, orgID).Scan(&customerID); err != nil {
		t.Fatalf("insert customer: %v", err)
	}
	insertKey := func(prefix string) {
		t.Helper()
		if _, err := conn.Exec(ctx, `INSERT INTO identity_proofing_api_keys (organization_id, customer_id, name, prefix, secret_hash)
			VALUES ($1, $2, $3, $3, $4)`, orgID, customerID, prefix, []byte(prefix)); err != nil {
			t.Fatalf("insert key %s: %v", prefix, err)
		}
	}
	insertKey("yp_live_old")

	if err := migrate.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	insertKey("yp_live_new")

	for _, prefix := range []string{"yp_live_old", "yp_live_new"} {
		var scopes []string
		if err := conn.QueryRow(ctx, `SELECT scopes FROM identity_proofing_api_keys WHERE prefix = $1`, prefix).Scan(&scopes); err != nil {
			t.Fatalf("read scopes %s: %v", prefix, err)
		}
		if !slices.Contains(scopes, "results:read") || !slices.Contains(scopes, "sessions:write") {
			t.Errorf("%s scopes = %v, want every scope", prefix, scopes)
		}
	}
}
