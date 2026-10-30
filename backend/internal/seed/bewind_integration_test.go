//go:build integration

package seed

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// TestEnsureBewindDemoSeedsAndIsIdempotent covers the staging bewind demo: the
// court, the office and Gemeente Nijmegen each get their schema, the office's
// director is its claimed legal representative, the mandate chain runs from
// the director through the team leader to one bewindvoerder per team, and a
// re-run duplicates none of it.
func TestEnsureBewindDemoSeedsAndIsIdempotent(t *testing.T) {
	pool, dsn := testdb.Fresh(t)
	ctx := context.Background()

	for range 2 {
		if err := EnsureBewindDemo(ctx, dsn, "qerds.localhost"); err != nil {
			t.Fatalf("EnsureBewindDemo: %v", err)
		}
	}

	orgID := func(slug string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, "SELECT id FROM organizations WHERE slug = $1", slug).Scan(&id); err != nil {
			t.Fatalf("query org %q: %v", slug, err)
		}
		return id
	}
	office, court, nijmegen := orgID(bewindOfficeSlug), orgID(bewindCourtSlug), orgID(nijmegenSlug)

	for _, c := range []struct{ org, vct string }{
		{court, bewindCourtCredential.schema.VCT},
		{nijmegen, bewindNijmegenCredential.schema.VCT},
		{office, bewindOfficeCredential.schema.VCT},
	} {
		assertBewindCount(t, ctx, pool, 1, "SELECT count(*) FROM attestation_schemas WHERE organization_id = $1 AND vct = $2", c.org, c.vct)
		assertBewindCount(t, ctx, pool, 1, "SELECT count(*) FROM org_issuer_settings WHERE organization_id = $1", c.org)
	}

	// Nijmegen is a partner tenant: the demo never gives it a representative.
	assertBewindCount(t, ctx, pool, 0, "SELECT count(*) FROM wallet_representations WHERE organization_id = $1", nijmegen)
	assertBewindCount(t, ctx, pool, 1,
		"SELECT count(*) FROM wallet_representations r JOIN users u ON u.id = r.claimed_by_user_id WHERE r.organization_id = $1 AND u.email = $2",
		office, bewindDirectorEmail)

	// One org-wide root grant, two department-scoped delegations under it.
	assertBewindCount(t, ctx, pool, 3, "SELECT count(*) FROM mandates WHERE organization_id = $1 AND revoked_at IS NULL", office)
	assertBewindCount(t, ctx, pool, 1, "SELECT count(*) FROM mandates WHERE organization_id = $1 AND parent_mandate_id IS NULL AND scope = 'organization'", office)
	assertBewindCount(t, ctx, pool, 2, "SELECT count(*) FROM mandates WHERE organization_id = $1 AND parent_mandate_id IS NOT NULL AND scope = 'department'", office)
	assertBewindCount(t, ctx, pool, 3, "SELECT count(*) FROM audit_events WHERE organization_id = $1 AND action = 'mandate.granted'", office)

	assertBewindCount(t, ctx, pool, len(yiviTeam)+len(bewindStaff), "SELECT count(*) FROM memberships WHERE organization_id = $1", office)
	assertBewindCount(t, ctx, pool, 4, "SELECT count(*) FROM qerds_contacts")
	assertBewindCount(t, ctx, pool, 1, "SELECT count(*) FROM credential_request_templates WHERE organization_id = $1", nijmegen)
}

// assertBewindCount is seed_test's assertCount for this internal test package.
func assertBewindCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want int, query string, args ...any) {
	t.Helper()
	var got int
	if err := pool.QueryRow(ctx, query, args...).Scan(&got); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	if got != want {
		t.Fatalf("count %q = %d, want %d", query, got, want)
	}
}
