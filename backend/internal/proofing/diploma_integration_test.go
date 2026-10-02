//go:build integration

package proofing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func TestFlowDiplomaStoreSavesAndAudits(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewFlowDiplomaStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	if mode, err := store.Get(ctx, orgID, "f1"); err != nil || mode != DiplomasOff {
		t.Errorf("Get unset = %q, %v; want off", mode, err)
	}
	for range 2 {
		if _, err := store.Save(ctx, orgID, "f1", DiplomasRequired); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	all, err := store.All(ctx, orgID)
	if err != nil || len(all) != 1 || all["f1"] != DiplomasRequired {
		t.Errorf("All = %v, %v; want f1 required", all, err)
	}
	if n := auditCount(t, pool, audit.IdentityProofingFlowDiplomasConfigured); n != 1 {
		t.Errorf("audited %d times, want once: saving the same mode changes nothing", n)
	}
}

func TestDiplomaStoreKeepsExtractsUntilPurged(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	customers := NewCustomerStore(pool, audit.NopRecorder{})
	requests := NewRequestStore(pool, audit.NewDBRecorder(), cipher)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	diplomas := NewDiplomaStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	customer, err := customers.Create(ctx, orgID, sam, "Hogeschool")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if _, _, err := webhooks.Save(ctx, orgID, customer.ID, "https://hooks.example.org/x", []string{EventSessionDiplomaAdded}); err != nil {
		t.Fatalf("Save webhook: %v", err)
	}
	in := newStoredRequest(orgID, sam, Subject{CustomerID: &customer.ID, Email: "anna@example.org"})
	in.Diplomas = DiplomasRequired
	req := createStarted(t, requests, in, "s1")
	if req.Diplomas != DiplomasRequired {
		t.Errorf("stored diplomas = %q, want required", req.Diplomas)
	}
	if err := requests.RecordOutcome(ctx, req, "s1", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}

	signed := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	extract := Diploma{
		DocumentType: "Diploma", Qualification: "HBO Bachelor Verpleegkunde", Profiles: []string{},
		Institution: "Hogeschool Utrecht", PlaceOfIssue: "Utrecht", DateAwarded: time.Date(2015, 7, 1, 0, 0, 0, 0, time.UTC),
		NLQFLevel: "6", EQFLevel: "6", DocumentNumber: "2896311", SignedAt: &signed,
	}
	if _, added, err := diplomas.Add(ctx, req, extract); err != nil || !added {
		t.Fatalf("Add = %v, %v; want added", added, err)
	}
	if _, added, err := diplomas.Add(ctx, req, extract); err != nil || added {
		t.Errorf("Add again = %v, %v; want the duplicate refused", added, err)
	}
	if err := diplomas.RecordRejected(ctx, req, "holder_mismatch", ""); err != nil {
		t.Fatalf("RecordRejected: %v", err)
	}
	held, err := diplomas.List(ctx, []uuid.UUID{req.ID})
	if err != nil || len(held[req.ID]) != 1 {
		t.Fatalf("List = %v, %v; want one extract", held, err)
	}
	got := held[req.ID][0]
	if got.Qualification != extract.Qualification || !got.DateAwarded.Equal(extract.DateAwarded) ||
		got.SignedAt == nil || !got.SignedAt.Equal(signed) {
		t.Errorf("held = %+v, want %+v", got, extract)
	}
	if auditCount(t, pool, audit.IdentityProofingDiplomaAdded) != 1 || auditCount(t, pool, audit.IdentityProofingDiplomaRejected) != 1 {
		t.Error("want one diploma_added and one diploma_rejected event")
	}
	deliveries, err := webhooks.Deliveries(ctx, orgID, customer.ID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != EventSessionDiplomaAdded {
		t.Errorf("deliveries = %+v, %v; want one session.diploma_added", deliveries, err)
	}

	if err := requests.Purge(ctx, req); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if held, err := diplomas.List(ctx, []uuid.UUID{req.ID}); err != nil || len(held[req.ID]) != 0 {
		t.Errorf("after purge List = %v, %v; want none", held, err)
	}
}
