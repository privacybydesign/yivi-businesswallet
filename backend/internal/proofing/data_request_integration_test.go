//go:build integration

package proofing

import (
	"bytes"
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

	// Only the customer's identity sessions with personal data and the
	// requester's identity match key are candidates: case, accents and the
	// date's notation aside.
	anna := SubjectIdentity{GivenName: "Anna", FamilyName: "Jansen", BirthDate: "1990-04-12"}
	if err := data.RecordIdentityKey(ctx, session, SubjectIdentity{FamilyName: "JANSÉN", BirthDate: "12-04-1990"}); err != nil {
		t.Fatalf("RecordIdentityKey session: %v", err)
	}
	other := newDataRequest(t, requests, orgID, sam, customer.ID, FlowIdentity, "s6")
	if err := requests.RecordOutcome(ctx, other, "s6", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Pietersen"}); err != nil {
		t.Fatalf("RecordOutcome other: %v", err)
	}
	if err := data.RecordIdentityKey(ctx, other, SubjectIdentity{FamilyName: "Pietersen", BirthDate: anna.BirthDate}); err != nil {
		t.Fatalf("RecordIdentityKey other: %v", err)
	}
	if candidates, err := data.Candidates(ctx, dr, SubjectIdentity{FamilyName: "Jansen"}); err != nil || len(candidates) != 0 {
		t.Fatalf("Candidates without a birth date = %+v, %v; want none", candidates, err)
	}
	candidates, err := data.Candidates(ctx, dr, anna)
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
	if err := data.RecordIdentityKey(ctx, dr, anna); err != nil {
		t.Fatalf("RecordIdentityKey data request: %v", err)
	}
	inReview := newDataRequest(t, requests, orgID, sam, customer.ID, FlowDataAccess, "s3")
	if err := requests.RecordOutcome(ctx, inReview, "s3", StatusNeedsReview,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome in review: %v", err)
	}
	if err := data.RecordIdentityKey(ctx, inReview, anna); err != nil {
		t.Fatalf("RecordIdentityKey in review: %v", err)
	}
	// Nor against a session sent to one known person and proofed by someone
	// else: it proves who that was, not whom it was sent to.
	mismatch := newDataRequest(t, requests, orgID, sam, customer.ID, FlowIdentity, "s5")
	if err := requests.RecordOutcome(ctx, mismatch, "s5", StatusRejected,
		proofingprovider.Result{Status: proofingprovider.StatusRejected, ErrorCode: errorIdentityMismatch}); err != nil {
		t.Fatalf("RecordOutcome mismatch: %v", err)
	}
	if err := data.RecordIdentityKey(ctx, mismatch, anna); err != nil {
		t.Fatalf("RecordIdentityKey mismatch: %v", err)
	}
	erasure := newDataRequest(t, requests, orgID, sam, customer.ID, FlowDataErasure, "s4")
	candidates, err = data.Candidates(ctx, erasure, anna)
	if err != nil || len(candidates) != 2 || candidates[0].ID != session.ID || candidates[1].ID != dr.ID {
		t.Fatalf("erasure Candidates = %+v, %v; want the session and the decided access request", candidates, err)
	}

	// A purge drops the key with the rest of the personal data.
	if other, err = requests.Get(ctx, orgID, other.ID); err != nil {
		t.Fatalf("Get other: %v", err)
	}
	if err := requests.Purge(ctx, other); err != nil {
		t.Fatalf("Purge other: %v", err)
	}
	var keyed bool
	if err := pool.QueryRow(ctx, `SELECT identity_match_key IS NOT NULL FROM identity_proofing_requests WHERE id = $1`,
		other.ID).Scan(&keyed); err != nil || keyed {
		t.Errorf("identity match key after the purge = %v, %v; want none", keyed, err)
	}

	// What a search found per candidate is kept, the first outcome only.
	for _, c := range []struct {
		id    uuid.UUID
		level MatchLevel
	}{{session.ID, MatchStrong}, {dr.ID, ""}, {session.ID, ""}} {
		if err := data.RecordMatchCheck(ctx, erasure, c.id, c.level); err != nil {
			t.Fatalf("RecordMatchCheck: %v", err)
		}
	}
	checks, err := data.MatchChecks(ctx, erasure)
	if err != nil || len(checks) != 2 || checks[session.ID] != MatchStrong || checks[dr.ID] != "" {
		t.Errorf("MatchChecks = %v, %v; want the session strong, the access request no match", checks, err)
	}

	// One search at a time, whichever replica asks.
	release, ok, err := data.TryLockMatching(ctx, erasure.ID)
	if err != nil || !ok {
		t.Fatalf("TryLockMatching = %v, %v; want the lock", ok, err)
	}
	if _, ok, err := data.TryLockMatching(ctx, erasure.ID); err != nil || ok {
		t.Errorf("second TryLockMatching = %v, %v; want it held", ok, err)
	}
	release()
	again, ok, err := data.TryLockMatching(ctx, erasure.ID)
	if err != nil || !ok {
		t.Fatalf("TryLockMatching after release = %v, %v; want the lock", ok, err)
	}
	again()
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

// An erasure also finds the customer's unfinished sessions, for matching by
// address or typed name; a settled one is matched on identity instead. Such a
// match is stored and listed after the identity matches.
func TestDataRequestUnfinishedCandidates(t *testing.T) {
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
	other := send("piet@example.org")
	erasure := newDataRequest(t, requests, orgID, sam, customer.ID, FlowDataErasure, "s2")

	candidates, err := data.UnfinishedCandidates(ctx, erasure)
	if err != nil || len(candidates) != 2 || candidates[0].ID != pending.ID || candidates[1].ID != other.ID {
		t.Fatalf("UnfinishedCandidates = %+v, %v; want both pending sessions, not the settled one", candidates, err)
	}
	if err := data.SaveMatches(ctx, erasure, []NewDataMatch{
		{RequestID: pending.ID, Level: MatchEmail}, {RequestID: other.ID, Level: MatchName}, {RequestID: settled.ID, Level: MatchStrong},
	}); err != nil {
		t.Fatalf("SaveMatches: %v", err)
	}
	matches, err := data.Matches(ctx, erasure)
	if err != nil || len(matches) != 3 || matches[0].Level != MatchStrong || matches[1].Level != MatchName || matches[2].Level != MatchEmail {
		t.Fatalf("Matches = %+v, %v; want the strong match, then the name one, then the e-mail one", matches, err)
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

// The identity match key is one person's at one customer: the same name and
// date of birth at another customer hashes apart, and a missing date to none.
func TestIdentityMatchKey(t *testing.T) {
	data := NewDataRequestStore(nil, nil, newTestCipher(t))
	anna := SubjectIdentity{FamilyName: "Jansen", BirthDate: "1990-04-12"}
	customerA, customerB := uuid.New(), uuid.New()
	key := func(customerID uuid.UUID, id SubjectIdentity) []byte {
		t.Helper()
		k, err := data.identityMatchKey(Request{CustomerID: &customerID}, id)
		if err != nil {
			t.Fatalf("identityMatchKey: %v", err)
		}
		return k
	}
	if a, again := key(customerA, anna), key(customerA, SubjectIdentity{FamilyName: " jansen ", BirthDate: "12-04-1990"}); !bytes.Equal(a, again) {
		t.Error("the same person at one customer hashed apart")
	}
	if bytes.Equal(key(customerA, anna), key(customerB, anna)) {
		t.Error("the same person at two customers hashed alike")
	}
	if k := key(customerA, SubjectIdentity{FamilyName: "Jansen"}); k != nil {
		t.Errorf("a key without a birth date = %x; want none", k)
	}
}
