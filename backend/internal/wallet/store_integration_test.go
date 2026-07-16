//go:build integration

package wallet_test

import (
	"context"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/wallet"
)

const stubKVK = "90001234"

// TestRegisterOrganization is the end-to-end regression for the atomic
// registration: one attestation must create the org (with KVK identity + digital
// address + active status), the owner membership, and the representation list
// (with the requester's own representation claimed) — all or nothing.
func TestRegisterOrganization(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()

	requester, err := user.NewStore(pool).Create(ctx, user.User{
		Email:      "alice@example.com",
		GivenNames: "Alice",
		LastName:   "Owner",
	})
	if err != nil {
		t.Fatalf("create requester: %v", err)
	}

	att, err := registryprovider.NewStubRegistry().Consult(ctx, stubKVK)
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}

	store := wallet.NewStore(pool, audit.NopRecorder{})
	const slug = "stub-co"
	const address = "kvk-" + stubKVK + "@qerds.localhost"

	org, err := store.RegisterOrganization(ctx, requester.ID, slug, address, att)
	if err != nil {
		t.Fatalf("RegisterOrganization: %v", err)
	}
	if org.Slug != slug || org.Name != att.LegalName || org.KVKNumber != stubKVK {
		t.Fatalf("org = %q/%q/%q, want %q/%q/%q", org.Slug, org.Name, org.KVKNumber, slug, att.LegalName, stubKVK)
	}
	if org.Status != organization.StatusActive {
		t.Fatalf("status = %q, want active", org.Status)
	}
	if org.DigitalAddress != address {
		t.Fatalf("digital address = %q, want %q", org.DigitalAddress, address)
	}

	// The requester is an admin member.
	var role string
	if err := pool.QueryRow(ctx, `SELECT role FROM memberships WHERE organization_id = $1 AND user_id = $2`, org.ID, requester.ID).Scan(&role); err != nil {
		t.Fatalf("owner membership missing: %v", err)
	}
	if role != organization.RoleAdmin {
		t.Fatalf("owner role = %q, want admin", role)
	}

	// The org's default digital address matches the wallet address.
	var defaultAddr string
	if err := pool.QueryRow(ctx, `SELECT address FROM qerds_addresses WHERE organization_id = $1 AND is_default`, org.ID).Scan(&defaultAddr); err != nil {
		t.Fatalf("default address missing: %v", err)
	}
	if defaultAddr != address {
		t.Fatalf("default address = %q, want %q", defaultAddr, address)
	}

	// Every representative is recorded; the requester's own one is claimed.
	reps, err := store.ListRepresentations(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListRepresentations: %v", err)
	}
	if len(reps) != len(att.Representatives) {
		t.Fatalf("representations = %d, want %d", len(reps), len(att.Representatives))
	}
	claimed := 0
	for _, r := range reps {
		if r.Claimed {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("claimed representations = %d, want 1 (the requester)", claimed)
	}
}

// TestRegisterOrganizationRejectsDuplicateKVK covers "one wallet per company": a
// second registration for the same KVK number is refused.
func TestRegisterOrganizationRejectsDuplicateKVK(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()

	requester, err := user.NewStore(pool).Create(ctx, user.User{Email: "bob@example.com", GivenNames: "Bob", LastName: "Owner"})
	if err != nil {
		t.Fatalf("create requester: %v", err)
	}
	att, err := registryprovider.NewStubRegistry().Consult(ctx, stubKVK)
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}
	store := wallet.NewStore(pool, audit.NopRecorder{})

	if _, err := store.RegisterOrganization(ctx, requester.ID, "first", "a@qerds.localhost", att); err != nil {
		t.Fatalf("first RegisterOrganization: %v", err)
	}
	_, err = store.RegisterOrganization(ctx, requester.ID, "second", "b@qerds.localhost", att)
	if err == nil {
		t.Fatal("expected a duplicate-KVK registration to fail")
	}
}

// TestSetStatus transitions an org/wallet (suspend).
func TestSetStatus(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()

	requester, err := user.NewStore(pool).Create(ctx, user.User{Email: "carol@example.com", GivenNames: "Carol", LastName: "Owner"})
	if err != nil {
		t.Fatalf("create requester: %v", err)
	}
	att, err := registryprovider.NewStubRegistry().Consult(ctx, stubKVK)
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}
	store := wallet.NewStore(pool, audit.NopRecorder{})
	org, err := store.RegisterOrganization(ctx, requester.ID, "carol-co", "c@qerds.localhost", att)
	if err != nil {
		t.Fatalf("RegisterOrganization: %v", err)
	}

	suspended, err := store.SetStatus(ctx, org.ID, organization.StatusSuspended)
	if err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if suspended.Status != organization.StatusSuspended {
		t.Fatalf("status = %q, want suspended", suspended.Status)
	}
}
