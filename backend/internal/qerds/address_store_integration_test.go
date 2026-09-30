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

// TestDeleteAddress covers the guardrails: a lone address is refused as the
// last remaining one, a default among several is refused as the default, an
// unknown or cross-org id is not-found, and a plain non-default delete
// succeeds and drops out of ListAddresses.
func TestDeleteAddress(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	orgID := seedOrg(t, ctx, pool, "acme")

	store := qerds.NewStore(pool, audit.NopRecorder{})

	first, err := store.ProvisionAddress(ctx, orgID, "one@qerds.localhost", true, "")
	if err != nil {
		t.Fatalf("provision first: %v", err)
	}

	// A lone address is both the default and the org's last one; the last-
	// remaining guard is what should fire, since there is nothing to promote.
	if err := store.DeleteAddress(ctx, orgID, first.ID); !errors.Is(err, qerds.ErrAddressLastRemaining) {
		t.Fatalf("delete lone address err = %v, want ErrAddressLastRemaining", err)
	}

	second, err := store.ProvisionAddress(ctx, orgID, "two@qerds.localhost", false, "")
	if err != nil {
		t.Fatalf("provision second: %v", err)
	}

	// The default cannot be deleted while another address exists to take over.
	if err := store.DeleteAddress(ctx, orgID, first.ID); !errors.Is(err, qerds.ErrAddressIsDefault) {
		t.Fatalf("delete default err = %v, want ErrAddressIsDefault", err)
	}

	// Unknown id is not-found.
	if err := store.DeleteAddress(ctx, orgID, uuid.New()); !errors.Is(err, qerds.ErrAddressNotFound) {
		t.Fatalf("unknown id err = %v, want ErrAddressNotFound", err)
	}

	// Another org cannot delete this org's address.
	otherOrgID := seedOrg(t, ctx, pool, "other")
	if err := store.DeleteAddress(ctx, otherOrgID, second.ID); !errors.Is(err, qerds.ErrAddressNotFound) {
		t.Fatalf("cross-org err = %v, want ErrAddressNotFound", err)
	}

	// The non-default address can be deleted, leaving only the default.
	if err := store.DeleteAddress(ctx, orgID, second.ID); err != nil {
		t.Fatalf("DeleteAddress: %v", err)
	}
	remaining, err := store.ListAddresses(ctx, orgID)
	if err != nil {
		t.Fatalf("ListAddresses: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != first.ID {
		t.Fatalf("remaining = %+v, want only %v", remaining, first.ID)
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
