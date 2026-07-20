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

// TestOrgIDsWithAddresses covers the poll worker's fan-out query: it returns
// exactly the organizations that have at least one digital address, each once.
func TestOrgIDsWithAddresses(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	store := qerds.NewStore(pool, audit.NopRecorder{})

	// No addresses provisioned yet: empty result.
	orgIDs, err := store.OrgIDsWithAddresses(ctx)
	if err != nil {
		t.Fatalf("OrgIDsWithAddresses (empty): %v", err)
	}
	if len(orgIDs) != 0 {
		t.Fatalf("orgIDs = %v, want none before any address is provisioned", orgIDs)
	}

	withOne := seedOrg(t, ctx, pool, "acme")
	withTwo := seedOrg(t, ctx, pool, "beta")
	seedOrg(t, ctx, pool, "gamma") // org with no QERDS address

	if _, err := store.ProvisionAddress(ctx, withOne, "acme@qerds.localhost", true, ""); err != nil {
		t.Fatalf("provision acme: %v", err)
	}
	if _, err := store.ProvisionAddress(ctx, withTwo, "beta1@qerds.localhost", true, ""); err != nil {
		t.Fatalf("provision beta1: %v", err)
	}
	// A second address for the same org must not duplicate it in the result.
	if _, err := store.ProvisionAddress(ctx, withTwo, "beta2@qerds.localhost", false, ""); err != nil {
		t.Fatalf("provision beta2: %v", err)
	}

	orgIDs, err = store.OrgIDsWithAddresses(ctx)
	if err != nil {
		t.Fatalf("OrgIDsWithAddresses: %v", err)
	}
	got := map[uuid.UUID]int{}
	for _, id := range orgIDs {
		got[id]++
	}
	if len(got) != 2 || got[withOne] != 1 || got[withTwo] != 1 {
		t.Fatalf("orgIDs = %v, want exactly acme and beta once each", orgIDs)
	}
}
