//go:build integration

package migrate_test

import (
	"context"
	"slices"
	"testing"

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

	for _, want := range []string{"sessions:write", "sessions:read", "results:read", "flows:read"} {
		if !slices.Contains(scopes, want) {
			t.Errorf("scopes = %v, missing %s", scopes, want)
		}
	}
}
