//go:build integration

package proofing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// An address item nobody decides lapses once due, its request's retention
// after it opened; the session keeps its outcome, and a late decision is
// refused.
func TestAddressReviewItemLapses(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	requests := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	pending := storeSelfDeclaredAddress(t, requests, orgID, sam, "s1")

	due, err := requests.NextReviewItemDue(ctx)
	if err != nil {
		t.Fatalf("NextReviewItemDue: %v", err)
	}
	wantDue := time.Now().Add(pending.NameRetention)
	if due.Before(wantDue.Add(-time.Minute)) || due.After(wantDue) {
		t.Fatalf("due = %v; want about %v, the request's retention from now", due, wantDue)
	}
	if n, err := requests.LapseReviewItems(ctx, due.Add(-time.Second), maxReconcilePerRound); err != nil || n != 0 {
		t.Fatalf("LapseReviewItems before due = %d, %v; want none", n, err)
	}
	if n, err := requests.LapseReviewItems(ctx, due, maxReconcilePerRound); err != nil || n != 1 {
		t.Fatalf("LapseReviewItems when due = %d, %v; want one", n, err)
	}

	assertAddressItemLapsed(t, pool, requests, pending, sam)
}

// assertAddressItemLapsed checks that pending's address item lapsed: the
// session kept its outcome, the lapse is audited, and a late decision by
// reviewer is refused.
func assertAddressItemLapsed(t *testing.T, pool *pgxpool.Pool, requests *RequestStore, pending Request, reviewer uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	got, err := requests.Get(ctx, pending.OrganizationID, pending.ID)
	if err != nil || got.AddressReview != AddressReviewLapsed || got.AddressReviewedAt == nil || got.Status != StatusApproved {
		t.Errorf("Get = %s %q at %v, %v; want approved, address lapsed", got.Status, got.AddressReview, got.AddressReviewedAt, err)
	}
	var lapsedCode string
	if err := pool.QueryRow(ctx, `SELECT metadata->'after'->>'errorCode' FROM audit_events WHERE action = $1`,
		audit.IdentityProofingReviewItemDecided).Scan(&lapsedCode); err != nil || lapsedCode != errorReviewLapsed {
		t.Errorf("review_item_decided errorCode = %q, %v; want %s", lapsedCode, err, errorReviewLapsed)
	}
	if at, err := requests.DecideStepReview(ctx, pending, ReviewKindAddress, reviewer, AddressReviewConfirmed, "Too late"); err != nil || at != nil {
		t.Errorf("DecideAddressReview after the lapse = %v, %v; want refused", at, err)
	}
	if next, err := requests.NextReviewItemDue(ctx); err != nil || !next.IsZero() {
		t.Errorf("NextReviewItemDue after the lapse = %v, %v; want none", next, err)
	}
	lapsed := AddressReviewLapsed
	if listed, err := requests.List(ctx, pending.OrganizationID, RequestFilter{ReviewKind: &addressKind, ReviewState: &lapsed}); err != nil || len(listed) != 1 {
		t.Errorf("List lapsed addresses = %d, %v; want the one", len(listed), err)
	}
}

// What waits is counted and listed per kind: a stored address item, and the
// requests whose own review waits (an identity check, a data request).
func TestReviewItemsCountedAndListedPerKind(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	requests := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()

	address := storeSelfDeclaredAddress(t, requests, orgID, sam, "s1")
	identity := storeInReview(t, requests, newStoredRequest(orgID, sam, externalSubject("bo@example.org")), "s2")
	dataIn := newStoredRequest(orgID, sam, externalSubject("cas@example.org"))
	dataIn.FlowKind = FlowDataAccess
	data := storeInReview(t, requests, dataIn, "s3")

	counts, err := requests.ReviewCounts(ctx, orgID)
	want := map[ReviewKind]int{ReviewKindIdentity: 1, ReviewKindAddress: 1, ReviewKindEmployment: 0, ReviewKindDataRequest: 1}
	if err != nil || len(counts) != len(want) {
		t.Fatalf("ReviewCounts = %v, %v; want %v", counts, err, want)
	}
	for kind, n := range want {
		if counts[kind] != n {
			t.Errorf("ReviewCounts[%s] = %d; want %d", kind, counts[kind], n)
		}
	}

	for kind, wantID := range map[ReviewKind]Request{ReviewKindIdentity: identity, ReviewKindAddress: address, ReviewKindDataRequest: data} {
		listed, err := requests.List(ctx, orgID, RequestFilter{ReviewKind: &kind})
		if err != nil || len(listed) != 1 || listed[0].ID != wantID.ID {
			t.Errorf("List %s = %d, %v; want only %s", kind, len(listed), err, wantID.ID)
		}
	}
	employment := ReviewKindEmployment
	if listed, err := requests.List(ctx, orgID, RequestFilter{ReviewKind: &employment}); err != nil || len(listed) != 0 {
		t.Errorf("List employment = %d, %v; want none", len(listed), err)
	}
}

// A purge drops a waiting item and the reason of a decided one.
func TestPurgeClearsReviewItems(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	requests := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	decided := storeSelfDeclaredAddress(t, requests, orgID, sam, "s1")
	if at, err := requests.DecideStepReview(ctx, decided, ReviewKindAddress, sam, AddressReviewRejected, "Anna lives elsewhere"); err != nil || at == nil {
		t.Fatalf("DecideAddressReview = %v, %v; want decided", at, err)
	}
	if err := requests.Purge(ctx, decided, nil); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	var state string
	var reason *string
	if err := pool.QueryRow(ctx, `SELECT state, reason FROM identity_proofing_review_items WHERE request_id = $1`,
		decided.ID).Scan(&state, &reason); err != nil || state != string(AddressReviewRejected) || reason != nil {
		t.Errorf("item after purge = %s, reason %v, %v; want rejected without a reason", state, reason, err)
	}
}

// storeInReview is a started request the engine sent to review.
func storeInReview(t *testing.T, requests *RequestStore, in NewStoredRequest, session string) Request {
	t.Helper()
	ctx := context.Background()
	req, err := requests.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ok, err := requests.AttachSession(ctx, req, attachedSession(session)); err != nil || !ok {
		t.Fatalf("AttachSession = %v, %v", ok, err)
	}
	if req, err = requests.Get(ctx, in.OrgID, in.ID); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := requests.RecordOutcome(ctx, req, session, StatusNeedsReview,
		proofingprovider.Result{Status: proofingprovider.StatusNeedsReview}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	return req
}
