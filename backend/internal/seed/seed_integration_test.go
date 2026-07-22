//go:build integration

package seed_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/seed"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// TestEnsureYiviOrganizationSeedsAndIsIdempotent covers the staging org seed: the
// Yivi organisation is present after one run with its team as admins, its three
// attestation schemas and its issuer settings; re-running does not duplicate any
// of it; and it pulls in none of the faker members or audit activity the full dev
// seed creates.
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

	// The four Yivi team members are admins of the org, with real names (not the
	// generic platform-admin placeholder) — and not duplicated on re-run.
	assertCount(t, ctx, pool, 4, "SELECT count(*) FROM memberships WHERE organization_id = $1 AND role = 'admin'", first.ID)
	assertCount(t, ctx, pool, 1,
		"SELECT count(*) FROM users u JOIN memberships m ON m.user_id = u.id WHERE m.organization_id = $1 AND u.email = 'd.mulder@yivi.app' AND u.given_names = 'Dibran' AND u.last_name = 'Mulder'",
		first.ID)

	// Yivi's attestation catalogue: the three schemas plus one issuer settings row.
	assertCount(t, ctx, pool, 3, "SELECT count(*) FROM attestation_schemas WHERE organization_id = $1", first.ID)
	assertCount(t, ctx, pool, 1, "SELECT count(*) FROM org_issuer_settings WHERE organization_id = $1", first.ID)

	// The org seed must not drag in the full dev demo data: only the four real
	// team members exist (no faker members), and the only audit history is the
	// attestation catalogue provisioning (schema/template creation) — none of the
	// invitation/membership churn the full dev seed fabricates.
	assertCount(t, ctx, pool, 4, "SELECT count(*) FROM memberships")
	assertCount(t, ctx, pool, 0, "SELECT count(*) FROM users WHERE email LIKE '%@example.test'")
	assertCount(t, ctx, pool, 0,
		"SELECT count(*) FROM audit_events WHERE action NOT IN ('attestation.schema_created', 'attestation.template_created')")
}

// TestEnsureKVKRegisterOrganizationSeedsAndIsIdempotent covers the staging seed of
// the KVK register participant (the authentic source): the org is present with its
// nl.kvk.registration schema and issuer settings after one run, re-running does not
// duplicate any of it, and it has no representative of its own.
func TestEnsureKVKRegisterOrganizationSeedsAndIsIdempotent(t *testing.T) {
	pool, dsn := testdb.Fresh(t)
	ctx := context.Background()

	first, err := seed.EnsureKVKRegisterOrganization(ctx, dsn)
	if err != nil {
		t.Fatalf("first EnsureKVKRegisterOrganization: %v", err)
	}
	if first.Slug != registryprovider.RegisterSlug {
		t.Fatalf("slug = %q, want %q", first.Slug, registryprovider.RegisterSlug)
	}
	if first.KVKNumber != registryprovider.RegisterKVKNumber {
		t.Fatalf("kvk number = %q, want %q", first.KVKNumber, registryprovider.RegisterKVKNumber)
	}

	second, err := seed.EnsureKVKRegisterOrganization(ctx, dsn)
	if err != nil {
		t.Fatalf("second EnsureKVKRegisterOrganization: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("re-run created a new org: %s != %s", second.ID, first.ID)
	}

	assertCount(t, ctx, pool, 1, "SELECT count(*) FROM organizations")
	// The register is the authentic source, not a consultable company: no representative.
	assertCount(t, ctx, pool, 0, "SELECT count(*) FROM wallet_representations WHERE organization_id = $1", first.ID)
	// Its nl.kvk.registration schema and issuer settings, not duplicated on re-run.
	assertCount(t, ctx, pool, 1, "SELECT count(*) FROM attestation_schemas WHERE organization_id = $1", first.ID)
	assertCount(t, ctx, pool, 1, "SELECT count(*) FROM org_issuer_settings WHERE organization_id = $1", first.ID)
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
