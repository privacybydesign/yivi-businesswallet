//go:build integration

package proofing

import (
	"context"
	"testing"

	"github.com/google/uuid"
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
	f := newSealedFixture(t, EventSessionAddressAdded)
	pool, requests, webhooks, orgID, in := f.pool, f.requests, f.webhooks, f.orgID, f.in
	ctx := context.Background()
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

func TestAddressMismatchWaitsForReview(t *testing.T) {
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
	if err != nil || got.Status != StatusApproved || got.ErrorCode != "" || got.AddressReview != AddressReviewPending {
		t.Fatalf("Get = %s %q %q, %v; want approved, address pending", got.Status, got.ErrorCode, got.AddressReview, err)
	}
	if got.SubjectAddress == nil || got.SubjectAddress.Registered == nil || *got.SubjectAddress.Registered != registered {
		t.Errorf("address = %+v; want it kept with the registered one for the reviewer", got.SubjectAddress)
	}
	assertMismatchReported(t, pool, webhooks, orgID)
	assertAddressPurged(t, pool, requests, got)
}

// assertMismatchReported checks that an address mismatch is audited, its
// verification without the address, and sent as session.address_added
// without the registered address, and that nothing failed the session.
func assertMismatchReported(t *testing.T, pool *pgxpool.Pool, webhooks *WebhookStore, orgID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if auditCount(t, pool, audit.IdentityProofingRejected) != 0 {
		t.Error("a mismatch rejected the session")
	}
	var audited bool
	if err := pool.QueryRow(ctx, `SELECT metadata::text LIKE '%"verification": "registered_mismatch"%'
		AND metadata::text NOT LIKE '%Molenstraat%' FROM audit_events WHERE action = $1`,
		audit.IdentityProofingAddressAdded).Scan(&audited); err != nil || !audited {
		t.Errorf("address_added audits the verification without the address (%v)", err)
	}
	deliveries, err := webhooks.Deliveries(ctx, orgID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != EventSessionAddressAdded {
		t.Fatalf("deliveries = %+v, %v; want only session.address_added", deliveries, err)
	}
	var withRegistered bool
	if err := pool.QueryRow(ctx, `SELECT payload::text LIKE '%Kerkstraat%' FROM identity_proofing_webhook_deliveries
		WHERE organization_id = $1`, orgID).Scan(&withRegistered); err != nil || withRegistered {
		t.Errorf("session.address_added carries the registered address (%v)", err)
	}
}

// startWithRegistered sends a request in an org of its own, as createStarted
// finds it as the org's one request, and keeps registered on it sealed.
func startWithRegistered(t *testing.T, pool *pgxpool.Pool, requests *RequestStore, sender uuid.UUID, org, sessionID string, registered Address) Request {
	t.Helper()
	ctx := context.Background()
	in := newStoredRequest(makeOrg(t, pool, org), sender, externalSubject("anna@example.org"))
	in.Address = AddressRequired
	req := createStarted(t, requests, in, sessionID)
	if err := requests.SetRegisteredAddress(ctx, req, &registered); err != nil {
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

	start := func(org, sessionID string) Request {
		return startWithRegistered(t, pool, requests, sam, org, sessionID, registered)
	}

	added := start("acme", "s1")
	if ok, err := requests.AddAddress(ctx, added, registered); err != nil || !ok {
		t.Fatalf("AddAddress = %v, %v; want added", ok, err)
	}
	if got, err := requests.RegisteredAddress(ctx, added.ID); err != nil || got != nil {
		t.Errorf("after AddAddress registered address = %+v, %v; want none", got, err)
	}
	if err := requests.SetRegisteredAddress(ctx, added, &registered); err != nil {
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

func TestSelfDeclaredAddressReviewStored(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	requests := NewRequestStore(pool, audit.NewDBRecorder(), cipher)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	reviewer := makeUser(t, pool, "rita@example.org")
	ctx := context.Background()
	if _, _, err := webhooks.Save(ctx, orgID, "https://hooks.example.org/x", []string{EventSessionAddressReviewed}); err != nil {
		t.Fatalf("Save webhook: %v", err)
	}
	pending := storeSelfDeclaredAddress(t, requests, orgID, sam, "s1")
	review := AddressReviewPending
	if listed, err := requests.List(ctx, orgID, RequestFilter{AddressReview: &review}); err != nil || len(listed) != 1 {
		t.Errorf("List pending addresses = %d, %v; want the one", len(listed), err)
	}

	if ok, err := requests.DecideAddressReview(ctx, pending, reviewer, AddressReviewConfirmed, "Matches the rental contract"); err != nil || !ok {
		t.Fatalf("DecideAddressReview = %v, %v; want decided", ok, err)
	}
	if ok, err := requests.DecideAddressReview(ctx, pending, reviewer, AddressReviewRejected, "Another address"); err != nil || ok {
		t.Errorf("DecideAddressReview again = %v, %v; want refused", ok, err)
	}
	got, err := requests.Get(ctx, orgID, pending.ID)
	if err != nil || got.AddressReview != AddressReviewConfirmed || got.Status != StatusApproved {
		t.Errorf("Get = %s %q, %v; want approved, address confirmed", got.Status, got.AddressReview, err)
	}
	assertAddressReviewReported(t, pool, webhooks, orgID)
}

// assertAddressReviewReported checks that a confirmed address decision is
// audited once with its reason, and sent once as session.address_reviewed
// carrying the decision.
func assertAddressReviewReported(t *testing.T, pool *pgxpool.Pool, webhooks *WebhookStore, orgID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if auditCount(t, pool, audit.IdentityProofingAddressReviewed) != 1 {
		t.Error("want one address_reviewed event")
	}
	var reasoned bool
	if err := pool.QueryRow(ctx, `SELECT metadata::text LIKE '%Matches the rental contract%' FROM audit_events
		WHERE action = $1`, audit.IdentityProofingAddressReviewed).Scan(&reasoned); err != nil || !reasoned {
		t.Errorf("address_reviewed does not audit the reason (%v)", err)
	}
	deliveries, err := webhooks.Deliveries(ctx, orgID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != EventSessionAddressReviewed {
		t.Fatalf("deliveries = %+v, %v; want one session.address_reviewed", deliveries, err)
	}
	var decided string
	if err := pool.QueryRow(ctx, `SELECT payload->>'addressReview' FROM identity_proofing_webhook_deliveries
		WHERE organization_id = $1`, orgID).Scan(&decided); err != nil || decided != string(AddressReviewConfirmed) {
		t.Errorf("session.address_reviewed data.addressReview = %q, %v; want confirmed", decided, err)
	}
}

// A purged request's address is erased, so it no longer waits on the Reviews
// page, however long its review was left pending.
func TestPurgedAddressNotListedForReview(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	requests := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	purged := storeSelfDeclaredAddress(t, requests, orgID, sam, "s1")
	if err := requests.Purge(ctx, purged, nil); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	review := AddressReviewPending
	if listed, err := requests.List(ctx, orgID, RequestFilter{AddressReview: &review}); err != nil || len(listed) != 0 {
		t.Errorf("List pending addresses = %d, %v; want none", len(listed), err)
	}
	if got, err := requests.Get(ctx, orgID, purged.ID); err != nil || got.AddressReview != "" {
		t.Errorf("Get after Purge = %q, %v; want no address review", got.AddressReview, err)
	}
}

// storeSelfDeclaredAddress is an approved external check whose address the
// register (BAG) found, so it waits for a reviewer.
func storeSelfDeclaredAddress(t *testing.T, requests *RequestStore, orgID, sentBy uuid.UUID, session string) Request {
	t.Helper()
	ctx := context.Background()
	in := newStoredRequest(orgID, sentBy, externalSubject("anna@example.org"))
	in.Address = AddressRequired
	req := createStarted(t, requests, in, session)
	if err := requests.RecordOutcome(ctx, req, session, StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	addr := Address{Street: "Kerkstraat", HouseNumber: "12A", PostalCode: "6511 AB", City: "Nijmegen", Country: "Nederland", Verification: AddressVerified}
	if added, err := requests.AddAddress(ctx, req, addr); err != nil || !added {
		t.Fatalf("AddAddress = %v, %v; want added", added, err)
	}
	pending, err := requests.Get(ctx, orgID, req.ID)
	if err != nil || pending.AddressReview != AddressReviewPending {
		t.Fatalf("Get = %q, %v; want the address pending", pending.AddressReview, err)
	}
	return pending
}
