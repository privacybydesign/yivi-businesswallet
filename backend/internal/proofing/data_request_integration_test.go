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

func TestDataRequestStore(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	recorder := audit.NewDBRecorder()
	customers := NewCustomerStore(pool, recorder)
	requests := NewRequestStore(pool, recorder, cipher)
	data := NewDataRequestStore(pool, recorder, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	customer, err := customers.Create(ctx, orgID, sam, "Initech")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}

	// A flow's kind defaults to an identity check, and is listed once set.
	if kind, err := data.FlowKind(ctx, orgID, "f-data"); err != nil || kind != FlowIdentity {
		t.Fatalf("FlowKind before = %q, %v; want identity", kind, err)
	}
	if _, err := data.SaveFlowKind(ctx, orgID, "f-data", FlowDataErasure); err != nil {
		t.Fatalf("SaveFlowKind: %v", err)
	}
	if kinds, err := data.AllFlowKinds(ctx, orgID); err != nil || len(kinds) != 1 || kinds["f-data"] != FlowDataErasure {
		t.Fatalf("AllFlowKinds = %v, %v; want f-data erasure", kinds, err)
	}
	if n := auditCount(t, pool, audit.IdentityProofingFlowKindConfigured); n != 1 {
		t.Errorf("flow_kind_configured audited %d times, want 1", n)
	}

	// The person's earlier session, and their data request.
	session := createStarted(t, requests, newStoredRequest(orgID, sam, Subject{CustomerID: &customer.ID, Email: "anna@example.org"}), "s1")
	if err := requests.RecordOutcome(ctx, session, "s1", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome session: %v", err)
	}
	in := newStoredRequest(orgID, sam, Subject{CustomerID: &customer.ID, Email: "anna@example.org"})
	in.FlowKind = FlowDataAccess
	dr, err := requests.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create data request: %v", err)
	}
	if ok, err := requests.AttachSession(ctx, dr, attachedSession("s2")); err != nil || !ok {
		t.Fatalf("AttachSession = %v, %v", ok, err)
	}
	if dr, err = requests.Get(ctx, orgID, dr.ID); err != nil || dr.FlowKind != FlowDataAccess {
		t.Fatalf("Get data request = %q, %v; want data_access", dr.FlowKind, err)
	}

	// Only the customer's identity sessions with personal data are candidates.
	candidates, err := data.Candidates(ctx, dr)
	if err != nil || len(candidates) != 1 || candidates[0].ID != session.ID || candidates[0].ProofedName != "Anna Jansen" {
		t.Fatalf("Candidates = %+v, %v; want the earlier session with its name", candidates, err)
	}
	if err := data.SaveMatches(ctx, dr, []NewDataMatch{{RequestID: session.ID, Level: MatchStrong}}); err != nil {
		t.Fatalf("SaveMatches: %v", err)
	}
	matches, err := data.Matches(ctx, dr)
	if err != nil || len(matches) != 1 || matches[0].Level != MatchStrong || matches[0].Status != StatusApproved ||
		matches[0].FlowName == "" || matches[0].Approved != nil {
		t.Fatalf("Matches = %+v, %v; want the strong match, undecided", matches, err)
	}
	if reqs, err := data.MatchedRequests(ctx, dr); err != nil || len(reqs) != 1 || reqs[0].ID != session.ID {
		t.Fatalf("MatchedRequests = %+v, %v; want the session", reqs, err)
	}

	until := time.Now().Add(DataExportWindow)
	if err := data.RecordDecision(ctx, dr, []uuid.UUID{session.ID}, &until); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	if matches, _ := data.Matches(ctx, dr); len(matches) != 1 || matches[0].Approved == nil || !*matches[0].Approved {
		t.Errorf("Matches after = %+v; want approved", matches)
	}
	if dr, err = requests.Get(ctx, orgID, dr.ID); err != nil || dr.DataExportUntil == nil {
		t.Errorf("export until = %v, %v; want set", dr.DataExportUntil, err)
	}
	if err := data.RecordExported(ctx, dr, 1); err != nil {
		t.Fatalf("RecordExported: %v", err)
	}
	if n := auditCount(t, pool, audit.IdentityProofingDataExported); n != 1 {
		t.Errorf("data_exported audited %d times, want 1", n)
	}
}
