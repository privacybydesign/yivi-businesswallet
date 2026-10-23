//go:build integration

package proofing

import (
	"bytes"
	"context"
	"strconv"
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
	requests := NewRequestStore(pool, recorder, cipher)
	data := NewDataRequestStore(pool, recorder, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()

	// A flow's kind defaults to an identity check. A flow the wallet created
	// for a data request is claimed as the org's standard flow of its kind; a
	// later claim of that kind leaves its flow a data request flow nothing runs
	// on, unless the standard flow it names is stale, which loses its mark.
	if kind, err := data.FlowKind(ctx, orgID, "f-data"); err != nil || kind != FlowIdentity {
		t.Fatalf("FlowKind before = %q, %v; want identity", kind, err)
	}
	if standard, err := data.ClaimStandardFlow(ctx, orgID, FlowDataErasure, "f-data", ""); err != nil || standard != "f-data" {
		t.Fatalf("ClaimStandardFlow = %q, %v; want f-data", standard, err)
	}
	if standard, err := data.ClaimStandardFlow(ctx, orgID, FlowDataErasure, "f-late", ""); err != nil || standard != "f-data" {
		t.Fatalf("ClaimStandardFlow second = %q, %v; want f-data kept", standard, err)
	}
	if kinds, err := data.AllFlowKinds(ctx, orgID); err != nil || len(kinds) != 2 || kinds["f-data"] != FlowDataErasure || kinds["f-late"] != FlowDataErasure {
		t.Fatalf("AllFlowKinds = %v, %v; want f-data and f-late erasure", kinds, err)
	}
	if standard, err := data.ClaimStandardFlow(ctx, orgID, FlowDataErasure, "f-new", "f-data"); err != nil || standard != "f-new" {
		t.Fatalf("ClaimStandardFlow over a stale one = %q, %v; want f-new", standard, err)
	}
	if standard, err := data.StandardFlows(ctx, orgID); err != nil || len(standard) != 1 || standard[FlowDataErasure] != "f-new" {
		t.Fatalf("StandardFlows = %v, %v; want only f-new for erasure", standard, err)
	}
	if n := auditCount(t, pool, audit.IdentityProofingFlowKindConfigured); n != 3 {
		t.Errorf("flow_kind_configured audited %d times, want 3", n)
	}

	// The person's earlier session, and their data request.
	session := createStarted(t, requests, newStoredRequest(orgID, sam, externalSubject("anna@example.org")), "s1")
	if err := requests.RecordOutcome(ctx, session, "s1", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome session: %v", err)
	}
	in := newStoredRequest(orgID, sam, externalSubject("anna@example.org"))
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

	// Only the org's external identity sessions with personal data and the
	// requester's identity match key are candidates: case, accents and the
	// date's notation aside; not a member's session, nor another org's.
	anna := SubjectIdentity{GivenName: "Anna", FamilyName: "Jansen", BirthDate: "1990-04-12"}
	if err := data.RecordIdentityKey(ctx, session, SubjectIdentity{FamilyName: "JANSÉN", BirthDate: "12-04-1990"}); err != nil {
		t.Fatalf("RecordIdentityKey session: %v", err)
	}
	other := newDataRequest(t, requests, orgID, sam, FlowIdentity, "s6")
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
	otherOrg := makeOrg(t, pool, "globex")
	for i, in := range []NewStoredRequest{
		newStoredRequest(orgID, sam, memberSubject(Member{UserID: sam, Name: "Sam", Email: "sam@example.org"})),
		newStoredRequest(otherOrg, sam, externalSubject("anna@example.org")),
	} {
		sid := "s-other-" + strconv.Itoa(i)
		held, err := requests.Create(ctx, in)
		if err != nil {
			t.Fatalf("Create %s: %v", sid, err)
		}
		if ok, err := requests.AttachSession(ctx, held, attachedSession(sid)); err != nil || !ok {
			t.Fatalf("AttachSession %s = %v, %v", sid, ok, err)
		}
		if err := requests.RecordOutcome(ctx, held, sid, StatusApproved,
			proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
			t.Fatalf("RecordOutcome %s: %v", sid, err)
		}
		if err := data.RecordIdentityKey(ctx, held, anna); err != nil {
			t.Fatalf("RecordIdentityKey %s: %v", sid, err)
		}
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
	if err := data.RecordDecision(ctx, dr, []uuid.UUID{session.ID}, &until, nil, nil); err != nil {
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
	inReview := newDataRequest(t, requests, orgID, sam, FlowDataAccess, "s3")
	if err := requests.RecordOutcome(ctx, inReview, "s3", StatusNeedsReview,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome in review: %v", err)
	}
	if err := data.RecordIdentityKey(ctx, inReview, anna); err != nil {
		t.Fatalf("RecordIdentityKey in review: %v", err)
	}
	// Nor against a session sent to one known person and proofed by someone
	// else: it proves who that was, not whom it was sent to.
	mismatch := newDataRequest(t, requests, orgID, sam, FlowIdentity, "s5")
	if err := requests.RecordOutcome(ctx, mismatch, "s5", StatusRejected,
		proofingprovider.Result{Status: proofingprovider.StatusRejected, ErrorCode: errorIdentityMismatch}); err != nil {
		t.Fatalf("RecordOutcome mismatch: %v", err)
	}
	if err := data.RecordIdentityKey(ctx, mismatch, anna); err != nil {
		t.Fatalf("RecordIdentityKey mismatch: %v", err)
	}
	erasure := newDataRequest(t, requests, orgID, sam, FlowDataErasure, "s4")
	candidates, err = data.Candidates(ctx, erasure, anna)
	if err != nil || len(candidates) != 2 || candidates[0].ID != session.ID || candidates[1].ID != dr.ID {
		t.Fatalf("erasure Candidates = %+v, %v; want the session and the decided access request", candidates, err)
	}

	// A check a reviewer rejected still holds whoever proofed it: each of the
	// reviewer's codes leaves it a candidate.
	rejected := map[uuid.UUID]bool{}
	for i, code := range reviewerRejectCodes {
		sid := "s-reviewed-" + strconv.Itoa(i)
		reviewed := newDataRequest(t, requests, orgID, sam, FlowIdentity, sid)
		if err := requests.RecordOutcome(ctx, reviewed, sid, StatusRejected,
			proofingprovider.Result{Status: proofingprovider.StatusRejected, ErrorCode: code}); err != nil {
			t.Fatalf("RecordOutcome %s: %v", code, err)
		}
		if err := data.RecordIdentityKey(ctx, reviewed, anna); err != nil {
			t.Fatalf("RecordIdentityKey %s: %v", code, err)
		}
		rejected[reviewed.ID] = true
	}
	later := newDataRequest(t, requests, orgID, sam, FlowDataAccess, "s-later")
	candidates, err = data.Candidates(ctx, later, anna)
	if err != nil {
		t.Fatalf("Candidates after the reviews: %v", err)
	}
	for _, c := range candidates {
		delete(rejected, c.ID)
	}
	if len(rejected) != 0 {
		t.Errorf("Candidates = %+v; want every check a reviewer rejected among them, missing %v", candidates, rejected)
	}

	// A purge drops the key with the rest of the personal data.
	if other, err = requests.Get(ctx, orgID, other.ID); err != nil {
		t.Fatalf("Get other: %v", err)
	}
	if err := requests.Purge(ctx, other, nil); err != nil {
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

// newDataRequest creates an external data request of kind with its session
// attached.
func newDataRequest(t *testing.T, requests *RequestStore, orgID, by uuid.UUID, kind FlowKind, sessionID string) Request {
	t.Helper()
	ctx := context.Background()
	in := newStoredRequest(orgID, by, externalSubject("anna@example.org"))
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

// An erasure also finds the org's unfinished external sessions, for matching by
// address or typed name; a settled one is matched on identity instead. Such a
// match is stored and listed after the identity matches.
func TestDataRequestUnfinishedCandidates(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	recorder := audit.NewDBRecorder()
	requests := NewRequestStore(pool, recorder, cipher)
	data := NewDataRequestStore(pool, recorder, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	send := func(email string) Request {
		t.Helper()
		req, err := requests.Create(ctx, newStoredRequest(orgID, sam, externalSubject(email)))
		if err != nil {
			t.Fatalf("Create %s: %v", email, err)
		}
		return req
	}

	// createStarted expects the org's first request.
	settled := createStarted(t, requests, newStoredRequest(orgID, sam, externalSubject("anna@example.org")), "s1")
	if err := requests.RecordOutcome(ctx, settled, "s1", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	pending := send("Anna@Example.org")
	other := send("piet@example.org")
	erasure := newDataRequest(t, requests, orgID, sam, FlowDataErasure, "s2")

	// A member's unfinished request, and another org's, are not candidates.
	if _, err := requests.Create(ctx, newStoredRequest(orgID, sam,
		memberSubject(Member{UserID: sam, Name: "Sam", Email: "sam@example.org"}))); err != nil {
		t.Fatalf("Create member request: %v", err)
	}
	if _, err := requests.Create(ctx, newStoredRequest(makeOrg(t, pool, "globex"), sam, externalSubject("anna@example.org"))); err != nil {
		t.Fatalf("Create other org's request: %v", err)
	}

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

// A data request's review lapses a calendar month after it went to review,
// the "Answer by" the Reviews page shows: 16 Oct is answered by 16 Nov, 1 Feb by
// 1 Mar (not 3 Mar, 30 days on), 31 Jan by the end of February.
func TestDataRequestReviewLapsesOnTheCalendar(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	requests := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	erasure := newDataRequest(t, requests, orgID, sam, FlowDataErasure, "s1")
	if err := requests.RecordOutcome(ctx, erasure, "s1", StatusNeedsReview,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome review: %v", err)
	}
	at := func(s string) time.Time {
		t.Helper()
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for proven, want := range map[string]string{
		"2026-10-16T14:40:00Z": "2026-11-16T14:40:00Z",
		"2027-02-01T09:00:00Z": "2027-03-01T09:00:00Z",
		"2027-01-31T09:00:00Z": "2027-02-28T09:00:00Z",
		"2028-01-31T09:00:00Z": "2028-02-29T09:00:00Z",
		"2027-03-31T23:30:00Z": "2027-04-30T23:30:00Z",
	} {
		if _, err := pool.Exec(ctx, `UPDATE identity_proofing_requests SET completed_at = $2 WHERE id = $1`,
			erasure.ID, at(proven)); err != nil {
			t.Fatalf("set when it went to review: %v", err)
		}
		// A raw write: the store would set purge_at in the same transaction.
		if err := refreshPurgeAt(ctx, pool, erasure.ID); err != nil {
			t.Fatalf("refresh purge time: %v", err)
		}
		got, err := requests.Get(ctx, orgID, erasure.ID)
		if err != nil || got.PurgeAt == nil || !got.PurgeAt.Equal(at(want)) {
			t.Errorf("in review since %s: purge at %v, %v; want %s", proven, got.PurgeAt, err, want)
		}
	}
}

// A data request left in review comes up for purge a month after it went to
// review (purge_at), not before, and can then be rejected there.
func TestDataRequestReviewLapses(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	recorder := audit.NewDBRecorder()
	requests := NewRequestStore(pool, recorder, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	erasure := newDataRequest(t, requests, orgID, sam, FlowDataErasure, "s1")
	if err := requests.RecordOutcome(ctx, erasure, "s1", StatusNeedsReview,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome review: %v", err)
	}
	// In review for a month, give or take a minute.
	inReviewFor := func(offset string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE identity_proofing_requests
			SET completed_at = now() - interval '1 month' + $2::interval WHERE id = $1`, erasure.ID, offset); err != nil {
			t.Fatalf("age the review: %v", err)
		}
		// A raw write: the store would set purge_at in the same transaction.
		if err := refreshPurgeAt(ctx, pool, erasure.ID); err != nil {
			t.Fatalf("refresh purge time: %v", err)
		}
	}

	inReviewFor("1 minute")
	if due, err := requests.ListPurgeDue(ctx, 10); err != nil || len(due) != 0 {
		t.Fatalf("purge due a minute before a month in review = %+v, %v; want none", due, err)
	}
	inReviewFor("-1 minute")
	due, err := requests.ListPurgeDue(ctx, 10)
	if err != nil || len(due) != 1 || due[0].ID != erasure.ID || due[0].Status != StatusNeedsReview {
		t.Fatalf("purge due a minute after a month in review = %+v, %v; want the request, still in review", due, err)
	}
	if err := requests.RecordOutcome(ctx, due[0], "s1", StatusRejected,
		proofingprovider.Result{Status: proofingprovider.StatusRejected, ErrorCode: errorReviewLapsed}); err != nil {
		t.Fatalf("RecordOutcome lapse: %v", err)
	}
	if got, err := requests.Get(ctx, orgID, erasure.ID); err != nil || got.Status != StatusRejected || got.ErrorCode != errorReviewLapsed {
		t.Errorf("after the lapse = %q %q, %v; want rejected %s", got.Status, got.ErrorCode, err, errorReviewLapsed)
	}
}

// The identity match key is one person's at one org: the same name and date
// of birth at another org hashes apart, and a missing date or a member's
// request to none.
func TestIdentityMatchKey(t *testing.T) {
	data := NewDataRequestStore(nil, nil, newTestCipher(t))
	anna := SubjectIdentity{FamilyName: "Jansen", BirthDate: "1990-04-12"}
	orgA, orgB := uuid.New(), uuid.New()
	key := func(orgID uuid.UUID, kind SubjectKind, id SubjectIdentity) []byte {
		t.Helper()
		k, err := data.identityMatchKey(Request{OrganizationID: orgID, SubjectKind: kind}, id)
		if err != nil {
			t.Fatalf("identityMatchKey: %v", err)
		}
		return k
	}
	if a, again := key(orgA, SubjectExternal, anna), key(orgA, SubjectExternal, SubjectIdentity{FamilyName: " jansen ", BirthDate: "12-04-1990"}); !bytes.Equal(a, again) {
		t.Error("the same person at one org hashed apart")
	}
	if bytes.Equal(key(orgA, SubjectExternal, anna), key(orgB, SubjectExternal, anna)) {
		t.Error("the same person at two orgs hashed alike")
	}
	if k := key(orgA, SubjectExternal, SubjectIdentity{FamilyName: "Jansen"}); k != nil {
		t.Errorf("a key without a birth date = %x; want none", k)
	}
	if k := key(orgA, SubjectMember, anna); k != nil {
		t.Errorf("a member's key = %x; want none", k)
	}
}
