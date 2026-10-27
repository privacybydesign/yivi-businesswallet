//go:build integration

package proofing

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

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
	requests := NewRequestStore(pool, audit.NewDBRecorder(), cipher)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	if _, _, err := webhooks.Save(ctx, orgID, "https://hooks.example.org/x", []string{EventSessionAddressAdded}); err != nil {
		t.Fatalf("Save webhook: %v", err)
	}
	in := newStoredRequest(orgID, sam, externalSubject("anna@example.org"))
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
	deliveries, err := webhooks.Deliveries(ctx, orgID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != EventSessionAddressAdded {
		t.Errorf("deliveries = %+v, %v; want one session.address_added", deliveries, err)
	}

	assertAddressPurged(t, pool, requests, got)
}

func TestAddressMismatchRejects(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	requests := NewRequestStore(pool, audit.NewDBRecorder(), cipher)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	if _, _, err := webhooks.Save(ctx, orgID, "https://hooks.example.org/x",
		[]string{EventSessionAddressAdded, EventSessionFailed}); err != nil {
		t.Fatalf("Save webhook: %v", err)
	}
	in := newStoredRequest(orgID, sam, externalSubject("anna@example.org"))
	in.Address = AddressRequired
	req := createStarted(t, requests, in, "s1")
	if err := requests.RecordOutcome(ctx, req, "s1", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	approved, err := requests.Get(ctx, orgID, req.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	registered := Address{Street: "Kerkstraat", HouseNumber: "12 A", PostalCode: "6511AB", City: "Nijmegen", Country: "Nederland"}
	addr := Address{
		Street: "Molenstraat", HouseNumber: "3", PostalCode: "6511 AB", City: "Nijmegen", Country: "Nederland",
		Verification: AddressRegisteredMismatch, Registered: &registered,
	}
	if added, err := requests.AddAddress(ctx, approved, addr); err != nil || !added {
		t.Fatalf("AddAddress = %v, %v; want added", added, err)
	}
	got, err := requests.Get(ctx, orgID, req.ID)
	if err != nil || got.Status != StatusRejected || got.ErrorCode != errorAddressMismatch {
		t.Fatalf("Get = %s %q, %v; want rejected %s", got.Status, got.ErrorCode, err, errorAddressMismatch)
	}
	if got.SubjectAddress == nil || got.SubjectAddress.Registered == nil || *got.SubjectAddress.Registered != registered {
		t.Errorf("address = %+v; want it kept with the registered one", got.SubjectAddress)
	}
	if auditCount(t, pool, audit.IdentityProofingRejected) != 1 {
		t.Error("want one rejected event")
	}
	var audited bool
	if err := pool.QueryRow(ctx, `SELECT metadata::text LIKE '%"verification": "registered_mismatch"%'
		AND metadata::text NOT LIKE '%Molenstraat%' FROM audit_events WHERE action = $1`,
		audit.IdentityProofingAddressAdded).Scan(&audited); err != nil || !audited {
		t.Errorf("address_added audits the verification without the address (%v)", err)
	}
	deliveries, err := webhooks.Deliveries(ctx, orgID)
	if err != nil || len(deliveries) != 2 {
		t.Fatalf("deliveries = %+v, %v; want session.address_added and session.failed", deliveries, err)
	}
	events := map[string]bool{deliveries[0].Event: true, deliveries[1].Event: true}
	if !events[EventSessionAddressAdded] || !events[EventSessionFailed] {
		t.Errorf("deliveries = %v; want session.address_added and session.failed", events)
	}
}

// assertAddressPurged purges req and checks that neither it nor a webhook
// delivery about it still carries the address.
func assertAddressPurged(t *testing.T, pool *pgxpool.Pool, requests *RequestStore, req Request) {
	t.Helper()
	ctx := context.Background()
	if err := requests.Purge(ctx, req, nil); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	purged, err := requests.Get(ctx, req.OrganizationID, req.ID)
	if err != nil || purged.SubjectAddress != nil {
		t.Errorf("after purge address = %+v, %v; want none", purged.SubjectAddress, err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity_proofing_webhook_deliveries
		WHERE request_id = $1 AND payload ? 'address'`, req.ID).Scan(&left); err != nil || left != 0 {
		t.Errorf("deliveries still carrying the address = %d (%v), want none", left, err)
	}
}

func TestRegisteredAddressSealedUntilAddressOrPurge(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	requests := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	registered := Address{Street: "Kerkstraat", HouseNumber: "12 A", PostalCode: "6511AB", City: "Nijmegen", Country: "Nederland"}

	// start sends a request in an org of its own, as createStarted finds it
	// as the org's one request.
	start := func(org, sessionID string) Request {
		in := newStoredRequest(makeOrg(t, pool, org), sam, externalSubject("anna@example.org"))
		in.Address = AddressRequired
		req := createStarted(t, requests, in, sessionID)
		if err := requests.SetRegisteredAddress(ctx, req, registered); err != nil {
			t.Fatalf("SetRegisteredAddress: %v", err)
		}
		got, err := requests.RegisteredAddress(ctx, req.ID)
		if err != nil || got == nil || *got != registered {
			t.Fatalf("RegisteredAddress = %+v, %v; want it kept", got, err)
		}
		var plain bool
		if err := pool.QueryRow(ctx, `SELECT position('Kerkstraat' in encode(registered_address_ciphertext, 'escape')) > 0
			FROM identity_proofing_requests WHERE id = $1`, req.ID).Scan(&plain); err != nil || plain {
			t.Errorf("registered address stored in the clear (%v)", err)
		}
		return req
	}

	added := start("acme", "s1")
	if ok, err := requests.AddAddress(ctx, added, registered); err != nil || !ok {
		t.Fatalf("AddAddress = %v, %v; want added", ok, err)
	}
	if got, err := requests.RegisteredAddress(ctx, added.ID); err != nil || got != nil {
		t.Errorf("after AddAddress registered address = %+v, %v; want none", got, err)
	}
	if err := requests.SetRegisteredAddress(ctx, added, registered); err != nil {
		t.Fatalf("SetRegisteredAddress after AddAddress: %v", err)
	}
	if got, _ := requests.RegisteredAddress(ctx, added.ID); got != nil {
		t.Error("a registered address was kept once the address was added")
	}

	purged := start("beta", "s2")
	if err := requests.Purge(ctx, purged, nil); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if got, err := requests.RegisteredAddress(ctx, purged.ID); err != nil || got != nil {
		t.Errorf("after purge registered address = %+v, %v; want none", got, err)
	}
}
