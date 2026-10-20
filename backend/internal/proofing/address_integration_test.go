//go:build integration

package proofing

import (
	"context"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func TestFlowAddressSavesAndAudits(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewFlowAddressStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	if mode, err := store.Get(ctx, orgID, "f1"); err != nil || mode != AddressOff {
		t.Errorf("Get unset = %q, %v; want off", mode, err)
	}
	for range 2 {
		if _, err := store.Save(ctx, orgID, "f1", AddressRequired); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	all, err := store.All(ctx, orgID)
	if err != nil || len(all) != 1 || all["f1"] != AddressRequired {
		t.Errorf("All = %v, %v; want f1 required", all, err)
	}
	if n := auditCount(t, pool, audit.IdentityProofingFlowAddressConfigured); n != 1 {
		t.Errorf("audited %d times, want once: saving the same mode changes nothing", n)
	}
}

func TestAddressSealedUntilPurge(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	customers := NewCustomerStore(pool, audit.NopRecorder{})
	requests := NewRequestStore(pool, audit.NewDBRecorder(), cipher)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	customer, err := customers.Create(ctx, orgID, sam, "Gemeente")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if _, _, err := webhooks.Save(ctx, orgID, customer.ID, "https://hooks.example.org/x", []string{EventSessionAddressAdded}); err != nil {
		t.Fatalf("Save webhook: %v", err)
	}
	in := newStoredRequest(orgID, sam, Subject{CustomerID: &customer.ID, Email: "anna@example.org"})
	in.Address = AddressRequired
	req := createStarted(t, requests, in, "s1")
	if req.Address != AddressRequired {
		t.Errorf("stored address mode = %q, want required", req.Address)
	}
	if err := requests.RecordOutcome(ctx, req, "s1", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}

	addr := Address{Street: "Kerkstraat", HouseNumber: "12A", PostalCode: "6511 AB", City: "Nijmegen", Country: "Nederland", Verification: AddressVerified}
	if added, err := requests.AddAddress(ctx, req, addr); err != nil || !added {
		t.Fatalf("AddAddress = %v, %v; want added", added, err)
	}
	if added, err := requests.AddAddress(ctx, req, addr); err != nil || added {
		t.Errorf("AddAddress again = %v, %v; want refused", added, err)
	}
	got, err := requests.Get(ctx, orgID, req.ID)
	if err != nil || got.SubjectAddress == nil || *got.SubjectAddress != addr || got.AddressAddedAt == nil {
		t.Fatalf("Get = %+v, %v; want the address", got.SubjectAddress, err)
	}
	var plain bool
	if err := pool.QueryRow(ctx, `SELECT position('Kerkstraat' in encode(address_ciphertext, 'escape')) > 0
		FROM identity_proofing_requests WHERE id = $1`, req.ID).Scan(&plain); err != nil || plain {
		t.Errorf("address stored in the clear (%v)", err)
	}
	if auditCount(t, pool, audit.IdentityProofingAddressAdded) != 1 {
		t.Error("want one address_added event")
	}
	deliveries, err := webhooks.Deliveries(ctx, orgID, customer.ID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != EventSessionAddressAdded {
		t.Errorf("deliveries = %+v, %v; want one session.address_added", deliveries, err)
	}

	if err := requests.Purge(ctx, got, nil); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	purged, err := requests.Get(ctx, orgID, req.ID)
	if err != nil || purged.SubjectAddress != nil {
		t.Errorf("after purge address = %+v, %v; want none", purged.SubjectAddress, err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity_proofing_webhook_deliveries
		WHERE request_id = $1 AND payload ? 'address'`, req.ID).Scan(&left); err != nil || left != 0 {
		t.Errorf("deliveries still carrying the address = %d (%v), want none", left, err)
	}
}
