//go:build integration

package qerds_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func seedOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug string) uuid.UUID {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ($1, $2, $3, $4, $5)`, slug, slug, "kvk-"+slug, "NL.KVK."+slug, slug+"@qerds.localhost"); err != nil {
		t.Fatalf("create org %q: %v", slug, err)
	}
	org, err := organization.NewStore(pool, audit.NopRecorder{}).GetBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("get org %q: %v", slug, err)
	}
	return org.ID
}

// TestSetDefaultAddress covers promoting an existing address to default: exactly
// one default holds, the previous default is cleared, promoting the current
// default is a no-op, and cross-org / unknown ids are rejected.
func TestSetDefaultAddress(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	orgID := seedOrg(t, ctx, pool, "acme")

	store := qerds.NewStore(pool, audit.NopRecorder{})

	// First address auto-defaults; second is non-default.
	first, err := store.ProvisionAddress(ctx, orgID, "one@qerds.localhost", true, "")
	if err != nil {
		t.Fatalf("provision first: %v", err)
	}
	second, err := store.ProvisionAddress(ctx, orgID, "two@qerds.localhost", false, "")
	if err != nil {
		t.Fatalf("provision second: %v", err)
	}

	def, err := store.DefaultAddress(ctx, orgID)
	if err != nil || def.ID != first.ID {
		t.Fatalf("default = %v (err %v), want %v", def.ID, err, first.ID)
	}

	// Promote the second: it becomes default, the first is cleared.
	promoted, err := store.SetDefaultAddress(ctx, orgID, second.ID)
	if err != nil {
		t.Fatalf("SetDefaultAddress: %v", err)
	}
	if !promoted.IsDefault || promoted.ID != second.ID {
		t.Fatalf("promoted = %+v, want second default", promoted)
	}
	def, err = store.DefaultAddress(ctx, orgID)
	if err != nil || def.ID != second.ID {
		t.Fatalf("default after promote = %v (err %v), want %v", def.ID, err, second.ID)
	}

	// Promoting the current default is a no-op that still returns it.
	same, err := store.SetDefaultAddress(ctx, orgID, second.ID)
	if err != nil || same.ID != second.ID || !same.IsDefault {
		t.Fatalf("re-promote = %+v (err %v), want second default", same, err)
	}

	// Unknown id is not-found.
	if _, err := store.SetDefaultAddress(ctx, orgID, uuid.New()); !errors.Is(err, qerds.ErrAddressNotFound) {
		t.Fatalf("unknown id err = %v, want ErrAddressNotFound", err)
	}

	// Another org cannot promote this org's address.
	otherOrgID := seedOrg(t, ctx, pool, "other")
	if _, err := store.SetDefaultAddress(ctx, otherOrgID, second.ID); !errors.Is(err, qerds.ErrAddressNotFound) {
		t.Fatalf("cross-org err = %v, want ErrAddressNotFound", err)
	}
}

// TestProvisionAddressCrossOrgCollision is the store-level defence for
// namespace ownership: the global uniqueness constraint stops a second org from
// provisioning an address another org already holds, surfaced as ErrAddressTaken.
func TestProvisionAddressCrossOrgCollision(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	acme := seedOrg(t, ctx, pool, "acme")
	other := seedOrg(t, ctx, pool, "other")

	store := qerds.NewStore(pool, audit.NopRecorder{})

	if _, err := store.ProvisionAddress(ctx, acme, "acme@qerds.localhost", true, ""); err != nil {
		t.Fatalf("provision for acme: %v", err)
	}
	if _, err := store.ProvisionAddress(ctx, other, "acme@qerds.localhost", true, ""); !errors.Is(err, qerds.ErrAddressTaken) {
		t.Fatalf("cross-org collision err = %v, want ErrAddressTaken", err)
	}
}
