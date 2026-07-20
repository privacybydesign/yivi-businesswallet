//go:build integration

package seed_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/seed"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// TestEnsureYiviOrganizationSeedsAndIsIdempotent covers the staging org seed:
// the Yivi organisation is present after one run, re-running does not duplicate
// it (nor its QERDS address or representative), and it pulls in none of the demo
// members or audit activity the full dev seed creates.
func TestEnsureYiviOrganizationSeedsAndIsIdempotent(t *testing.T) {
	pool, dsn := testdb.Fresh(t)
	ctx := context.Background()

	first, err := seed.EnsureYiviOrganization(ctx, dsn)
	if err != nil {
		t.Fatalf("first EnsureYiviOrganization: %v", err)
	}
	if first.Slug != "yivi" {
		t.Fatalf("slug = %q, want %q", first.Slug, "yivi")
	}
	if first.Name != "Yivi B.V." {
		t.Fatalf("name = %q, want %q", first.Name, "Yivi B.V.")
	}

	// Re-run: the staging deploy runs the seed every time, so this must be safe.
	second, err := seed.EnsureYiviOrganization(ctx, dsn)
	if err != nil {
		t.Fatalf("second EnsureYiviOrganization: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("re-run created a new org: %s != %s", second.ID, first.ID)
	}

	// No duplicate org, address or representative rows.
	assertCount(t, ctx, pool, 1, "SELECT count(*) FROM organizations")
	assertCount(t, ctx, pool, 1, "SELECT count(*) FROM qerds_addresses WHERE organization_id = $1", first.ID)
	assertCount(t, ctx, pool, 1, "SELECT count(*) FROM wallet_representations WHERE organization_id = $1", first.ID)

	// The org seed must not drag in the full dev demo data.
	assertCount(t, ctx, pool, 0, "SELECT count(*) FROM memberships")
	assertCount(t, ctx, pool, 0, "SELECT count(*) FROM audit_events")
}

func assertCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want int, query string, args ...any) {
	t.Helper()
	var got int
	if err := pool.QueryRow(ctx, query, args...).Scan(&got); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	if got != want {
		t.Fatalf("count %q = %d, want %d", query, got, want)
	}
}
