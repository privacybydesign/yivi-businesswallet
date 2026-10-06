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

	// A later erasure request is matched against the decided access request
	// too (it holds who asked), but never against a data request still in
	// review.
	if err := requests.RecordOutcome(ctx, dr, "s2", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome data request: %v", err)
	}
	inReview := newDataRequest(t, requests, orgID, sam, customer.ID, FlowDataAccess, "s3")
	if err := requests.RecordOutcome(ctx, inReview, "s3", StatusNeedsReview,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome in review: %v", err)
	}
	erasure := newDataRequest(t, requests, orgID, sam, customer.ID, FlowDataErasure, "s4")
	candidates, err = data.Candidates(ctx, erasure)
	if err != nil || len(candidates) != 2 || candidates[0].ID != session.ID || candidates[1].ID != dr.ID {
		t.Fatalf("erasure Candidates = %+v, %v; want the session and the decided access request", candidates, err)
	}
}

// newDataRequest creates a data request of kind for customerID with its
// session attached.
func newDataRequest(t *testing.T, requests *RequestStore, orgID, by, customerID uuid.UUID, kind FlowKind, sessionID string) Request {
	t.Helper()
	ctx := context.Background()
	in := newStoredRequest(orgID, by, Subject{CustomerID: &customerID, Email: "anna@example.org"})
	in.FlowKind = kind
	req, err := requests.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create %s: %v", kind, err)
	}
	if ok, err := requests.AttachSession(ctx, req, attachedSession(sessionID)); err != nil || !ok {
		t.Fatalf("AttachSession %s = %v, %v", kind, ok, err)
	}
	if req, err = requests.Get(ctx, orgID, req.ID); err != nil {
		t.Fatalf("Get %s: %v", kind, err)
	}
	return req
}

// An erasure also finds the customer's unfinished sessions sent to the same
// address, case aside; a settled one is matched on identity instead, and one
// to another address is not the person's. Such a match is stored and listed
// after the identity matches.
func TestDataRequestEmailCandidates(t *testing.T) {
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
	send := func(email string) Request {
		t.Helper()
		req, err := requests.Create(ctx, newStoredRequest(orgID, sam, Subject{CustomerID: &customer.ID, Email: email}))
		if err != nil {
			t.Fatalf("Create %s: %v", email, err)
		}
		return req
	}

	// createStarted expects the org's first request.
	settled := createStarted(t, requests, newStoredRequest(orgID, sam, Subject{CustomerID: &customer.ID, Email: "anna@example.org"}), "s1")
	if err := requests.RecordOutcome(ctx, settled, "s1", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	pending := send("Anna@Example.org")
	send("piet@example.org")
	erasure := newDataRequest(t, requests, orgID, sam, customer.ID, FlowDataErasure, "s2")

	candidates, err := data.EmailCandidates(ctx, erasure)
	if err != nil || len(candidates) != 1 || candidates[0].ID != pending.ID {
		t.Fatalf("EmailCandidates = %+v, %v; want only the pending session to the same address", candidates, err)
	}
	if err := data.SaveMatches(ctx, erasure, []NewDataMatch{
		{RequestID: pending.ID, Level: MatchEmail}, {RequestID: settled.ID, Level: MatchStrong},
	}); err != nil {
		t.Fatalf("SaveMatches: %v", err)
	}
	matches, err := data.Matches(ctx, erasure)
	if err != nil || len(matches) != 2 || matches[0].Level != MatchStrong || matches[1].Level != MatchEmail {
		t.Fatalf("Matches = %+v, %v; want the strong match, then the e-mail one", matches, err)
	}
}

// A data request left in review comes up for purge a month after it went to
// review (30 days, purge_at), not before, and can then be rejected there.
func TestDataRequestReviewLapses(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	recorder := audit.NewDBRecorder()
	customers := NewCustomerStore(pool, recorder)
	requests := NewRequestStore(pool, recorder, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	customer, err := customers.Create(ctx, orgID, sam, "Initech")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	erasure := newDataRequest(t, requests, orgID, sam, customer.ID, FlowDataErasure, "s1")
	if err := requests.RecordOutcome(ctx, erasure, "s1", StatusNeedsReview,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome review: %v", err)
	}
	inReviewFor := func(days int) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE identity_proofing_requests SET completed_at = now() - make_interval(days => $2)
			WHERE id = $1`, erasure.ID, days); err != nil {
			t.Fatalf("age the review: %v", err)
		}
		// A raw write: the store would set purge_at in the same transaction.
		if err := refreshPurgeAt(ctx, pool, erasure.ID); err != nil {
			t.Fatalf("refresh purge time: %v", err)
		}
	}

	inReviewFor(29)
	if due, err := requests.ListPurgeDue(ctx, 10); err != nil || len(due) != 0 {
		t.Fatalf("purge due after 29 days in review = %+v, %v; want none", due, err)
	}
	inReviewFor(31)
	due, err := requests.ListPurgeDue(ctx, 10)
	if err != nil || len(due) != 1 || due[0].ID != erasure.ID || due[0].Status != StatusNeedsReview {
		t.Fatalf("purge due after 31 days in review = %+v, %v; want the request, still in review", due, err)
	}
	if err := requests.RecordOutcome(ctx, due[0], "s1", StatusRejected,
		proofingprovider.Result{Status: proofingprovider.StatusRejected, ErrorCode: errorReviewLapsed}); err != nil {
		t.Fatalf("RecordOutcome lapse: %v", err)
	}
	if got, err := requests.Get(ctx, orgID, erasure.ID); err != nil || got.Status != StatusRejected || got.ErrorCode != errorReviewLapsed {
		t.Errorf("after the lapse = %q %q, %v; want rejected %s", got.Status, got.ErrorCode, err, errorReviewLapsed)
	}
}
