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

// TestActivateFromAttestation is the end-to-end regression for the atomic
// bootstrap: one attestation must create the org, its default address, the
// owner membership, the full representation list (with the requester's own
// representation claimed) and flip the instance to active — all or nothing.
func TestActivateFromAttestation(t *testing.T) {
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

	store := wallet.NewStore(pool, audit.NopRecorder{})
	const address = "kvk-" + stubKVK + "@qerds.localhost"

	inst, err := store.CreateInstance(ctx, requester.ID, stubKVK, address)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if inst.Status != wallet.StatusProvisioning {
		t.Fatalf("status = %q, want provisioning", inst.Status)
	}
	if err := store.MarkRequested(ctx, inst.ID); err != nil {
		t.Fatalf("MarkRequested: %v", err)
	}

	att := registryprovider.BuildStubAttestation(registryprovider.RegistrationRequest{
		PID:       registryprovider.PID{GivenNames: "Alice", FamilyName: "Owner", DateOfBirth: "1980-01-02"},
		KVKNumber: stubKVK,
	})

	active, err := store.ActivateFromAttestation(ctx, inst.ID, att)
	if err != nil {
		t.Fatalf("ActivateFromAttestation: %v", err)
	}
	if active.Status != wallet.StatusActive {
		t.Fatalf("status = %q, want active", active.Status)
	}
	if active.OrganizationID == nil {
		t.Fatal("organizationId is nil after activation")
	}
	if active.LegalName != att.LegalName || active.EUID != att.EUID {
		t.Fatalf("identity = %q/%q, want %q/%q", active.LegalName, active.EUID, att.LegalName, att.EUID)
	}
	orgID := *active.OrganizationID

	// The org exists and the requester is an admin member.
	if _, err := organization.NewStore(pool, audit.NopRecorder{}).GetByID(ctx, orgID); err != nil {
		t.Fatalf("org not created: %v", err)
	}
	var role string
	if err := pool.QueryRow(ctx, `SELECT role FROM memberships WHERE organization_id = $1 AND user_id = $2`, orgID, requester.ID).Scan(&role); err != nil {
		t.Fatalf("owner membership missing: %v", err)
	}
	if role != organization.RoleAdmin {
		t.Fatalf("owner role = %q, want admin", role)
	}

	// The org's default digital address is the wallet's provisioning address.
	var defaultAddr string
	if err := pool.QueryRow(ctx, `SELECT address FROM qerds_addresses WHERE organization_id = $1 AND is_default`, orgID).Scan(&defaultAddr); err != nil {
		t.Fatalf("default address missing: %v", err)
	}
	if defaultAddr != address {
		t.Fatalf("default address = %q, want %q", defaultAddr, address)
	}

	// Every representative is recorded; the requester's own one is claimed.
	reps, err := store.ListRepresentations(ctx, orgID)
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

// TestRejectInstance covers the not-a-representative path: no org is created and
// the instance is left rejected with a reason.
func TestRejectInstance(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()

	requester, err := user.NewStore(pool).Create(ctx, user.User{
		Email:      "bob@example.com",
		GivenNames: "Bob",
		LastName:   "Outsider",
	})
	if err != nil {
		t.Fatalf("create requester: %v", err)
	}

	store := wallet.NewStore(pool, audit.NopRecorder{})
	inst, err := store.CreateInstance(ctx, requester.ID, stubKVK, "kvk-"+stubKVK+"@qerds.localhost")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := store.MarkRequested(ctx, inst.ID); err != nil {
		t.Fatalf("MarkRequested: %v", err)
	}

	rejected, err := store.RejectInstance(ctx, inst.ID, wallet.RejectNotRepresentative)
	if err != nil {
		t.Fatalf("RejectInstance: %v", err)
	}
	if rejected.Status != wallet.StatusRejected {
		t.Fatalf("status = %q, want rejected", rejected.Status)
	}
	if rejected.RejectReason != wallet.RejectNotRepresentative {
		t.Fatalf("reason = %q, want %q", rejected.RejectReason, wallet.RejectNotRepresentative)
	}
	if rejected.OrganizationID != nil {
		t.Fatal("organizationId set on a rejected instance")
	}

	var orgs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM organizations`).Scan(&orgs); err != nil {
		t.Fatalf("count orgs: %v", err)
	}
	if orgs != 0 {
		t.Fatalf("orgs created = %d, want 0", orgs)
	}
}
