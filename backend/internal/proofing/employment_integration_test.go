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

func TestFlowEmploymentSaveAudit(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewFlowEmploymentStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	if mode, err := store.Get(ctx, orgID, "f1"); err != nil || mode != EmploymentOff {
		t.Errorf("Get unset = %q, %v; want off", mode, err)
	}
	for range 2 {
		if _, err := store.Save(ctx, orgID, "f1", EmploymentRequired); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	all, err := store.All(ctx, orgID)
	if err != nil || len(all) != 1 || all["f1"] != EmploymentRequired {
		t.Errorf("All = %v, %v; want f1 required", all, err)
	}
	if n := auditCount(t, pool, audit.IdentityProofingFlowEmploymentConfigured); n != 1 {
		t.Errorf("audited %d times, want once: saving the same mode changes nothing", n)
	}
}

func TestEmploymentSealedTillPurge(t *testing.T) {
	f := newSealedFixture(t, "Casino", EventSessionEmploymentAdded)
	pool, requests, webhooks, orgID, in := f.pool, f.requests, f.webhooks, f.orgID, f.in
	ctx := context.Background()
	in.Employment = EmploymentRequired
	req := createStarted(t, requests, in, "s1")
	if req.Employment != EmploymentRequired {
		t.Errorf("stored employment mode = %q, want required", req.Employment)
	}
	if err := requests.RecordOutcome(ctx, req, "s1", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}

	if err := requests.RecordEmploymentRejected(ctx, req, "holder_mismatch", ""); err != nil {
		t.Fatalf("RecordEmploymentRejected: %v", err)
	}
	emp := Employment{StatementDate: "2026-10-15", Relations: []EmploymentRelation{{
		Employer: "VOORBEELD B.V.", PayrollTaxNumber: "123456789L01", ContractForm: "schriftelijke arbeidsovereenkomst voor onbepaalde tijd",
		From: "2025-01-01", To: "2026-09-30",
	}}}
	if added, err := requests.AddEmployment(ctx, req, emp); err != nil || !added {
		t.Fatalf("AddEmployment = %v, %v; want added", added, err)
	}
	if added, err := requests.AddEmployment(ctx, req, emp); err != nil || added {
		t.Errorf("AddEmployment again = %v, %v; want refused", added, err)
	}
	got, err := requests.Get(ctx, orgID, req.ID)
	if err != nil || got.SubjectEmployment == nil || got.SubjectEmployment.Relations[0] != emp.Relations[0] || got.EmploymentAddedAt == nil {
		t.Fatalf("Get = %+v, %v; want the employment", got.SubjectEmployment, err)
	}
	checkEmploymentSealed(t, pool, webhooks, got)

	if err := requests.Purge(ctx, got, nil); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	checkEmploymentPurged(t, pool, requests, got)
}

// checkEmploymentSealed checks req's employment is stored sealed, audited and
// sent once.
func checkEmploymentSealed(t *testing.T, pool *pgxpool.Pool, webhooks *WebhookStore, req Request) {
	t.Helper()
	ctx := context.Background()
	var plain bool
	if err := pool.QueryRow(ctx, `SELECT position('VOORBEELD' in encode(employment_ciphertext, 'escape')) > 0
		FROM identity_proofing_requests WHERE id = $1`, req.ID).Scan(&plain); err != nil || plain {
		t.Errorf("employment stored in the clear (%v)", err)
	}
	if auditCount(t, pool, audit.IdentityProofingEmploymentAdded) != 1 || auditCount(t, pool, audit.IdentityProofingEmploymentRejected) != 1 {
		t.Error("want one employment_added and one employment_rejected event")
	}
	deliveries, err := webhooks.Deliveries(ctx, req.OrganizationID, *req.CustomerID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != EventSessionEmploymentAdded {
		t.Errorf("deliveries = %+v, %v; want one session.employment_added", deliveries, err)
	}
}

// checkEmploymentPurged checks a purge left none of req's employment behind.
func checkEmploymentPurged(t *testing.T, pool *pgxpool.Pool, requests *RequestStore, req Request) {
	t.Helper()
	ctx := context.Background()
	purged, err := requests.Get(ctx, req.OrganizationID, req.ID)
	if err != nil || purged.SubjectEmployment != nil {
		t.Errorf("after purge employment = %+v, %v; want none", purged.SubjectEmployment, err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity_proofing_webhook_deliveries
		WHERE request_id = $1 AND payload ? 'employment'`, req.ID).Scan(&left); err != nil || left != 0 {
		t.Errorf("deliveries still carrying the employment = %d (%v), want none", left, err)
	}
}

// approvedRequest stores in, started as session and approved by the engine.
func approvedRequest(t *testing.T, requests *RequestStore, in NewStoredRequest, session string) Request {
	t.Helper()
	ctx := context.Background()
	req := createStarted(t, requests, in, session)
	if err := requests.RecordOutcome(ctx, req, session, StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	approved, err := requests.Get(ctx, req.OrganizationID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	return approved
}

// A kept statement without a current job puts the approved request in review,
// in the same transaction, and it lists as an open review.
func TestNoCurrentEmploymentOpensReview(t *testing.T) {
	f := newSealedFixture(t, "Casino", EventSessionReviewOpened)
	ctx := context.Background()
	f.in.Employment = EmploymentRequired
	approved := approvedRequest(t, f.requests, f.in, "s1")

	emp := Employment{StatementDate: "2026-10-15", Relations: []EmploymentRelation{{
		Kind: "benefit", Employer: "WW-Uitkering van UWV", PayrollTaxNumber: "810220350L02", From: "2026-06-01", To: "2026-09-30",
	}}}
	if added, err := f.requests.AddEmployment(ctx, approved, emp); err != nil || !added {
		t.Fatalf("AddEmployment = %v, %v; want added", added, err)
	}
	got, err := f.requests.Get(ctx, f.orgID, approved.ID)
	if err != nil || got.Status != StatusNeedsReview || got.ReviewReason != ReviewNoCurrentEmployment {
		t.Fatalf("Get = %s (%q), %v; want needs_review for no current employment", got.Status, got.ReviewReason, err)
	}
	if auditCount(t, f.pool, audit.IdentityProofingNeedsReview) != 1 {
		t.Error("want one needs_review event")
	}
	deliveries, err := f.webhooks.Deliveries(ctx, f.orgID, f.customer.ID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != EventSessionReviewOpened {
		t.Errorf("deliveries = %+v, %v; want one session.review_opened", deliveries, err)
	}
	open, err := f.requests.ListOpenReviews(ctx, f.orgID, nil)
	if err != nil || len(open) != 1 || open[0].ID != approved.ID {
		t.Errorf("open reviews = %d, %v; want the request", len(open), err)
	}
	checkWalletDecidesReview(t, f.requests, got)
}

// checkWalletDecidesReview checks that the engine's approval, read again, does
// not take the wallet's review of got out of review, and the wallet's
// decision does.
func checkWalletDecidesReview(t *testing.T, requests *RequestStore, got Request) {
	t.Helper()
	ctx := context.Background()
	if err := requests.RecordOutcome(ctx, got, "s1", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved}); err != nil {
		t.Fatal(err)
	}
	if still, err := requests.Get(ctx, got.OrganizationID, got.ID); err != nil || still.Status != StatusNeedsReview {
		t.Fatalf("after the engine's outcome = %s, %v; want still in review", still.Status, err)
	}
	if err := requests.RecordWalletOutcome(ctx, got, "s1", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved}); err != nil {
		t.Fatal(err)
	}
	if decided, err := requests.Get(ctx, got.OrganizationID, got.ID); err != nil || decided.Status != StatusApproved {
		t.Errorf("after the wallet's decision = %s, %v; want approved", decided.Status, err)
	}
}

// approvedSince stores a request on f for session, approved approvedAgo.
func approvedSince(t *testing.T, f sealedFixture, session string, approvedAgo time.Duration) Request {
	t.Helper()
	ctx := context.Background()
	in := f.in
	in.ID, in.Subject.Email, in.Employment = uuid.New(), session+"@example.org", EmploymentRequired
	created, err := f.requests.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ok, err := f.requests.AttachSession(ctx, created, attachedSession(session)); err != nil || !ok {
		t.Fatalf("AttachSession = %v, %v", ok, err)
	}
	req, err := f.requests.Get(ctx, f.orgID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.requests.RecordOutcome(ctx, req, session, StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE identity_proofing_requests SET completed_at = now() - make_interval(secs => $2)
		WHERE id = $1`, req.ID, approvedAgo.Seconds()); err != nil {
		t.Fatal(err)
	}
	got, err := f.requests.Get(ctx, f.orgID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// An approved request without a Verzekeringsbericht is rejected once the
// window passed, audited and sent; one that holds a statement, or is still in
// its window, is left.
func TestRejectMissingEmployment(t *testing.T) {
	f := newSealedFixture(t, "Casino", EventSessionFailed)
	ctx := context.Background()
	missed := approvedSince(t, f, "s1", EmploymentWindow+time.Minute)
	inWindow := approvedSince(t, f, "s2", time.Minute)
	held := approvedSince(t, f, "s3", EmploymentWindow+time.Minute)
	if added, err := f.requests.AddEmployment(ctx, held, Employment{StatementDate: "2026-10-15", Employed: true, Relations: []EmploymentRelation{}}); err != nil || !added {
		t.Fatalf("AddEmployment = %v, %v", added, err)
	}

	next, err := f.requests.NextMissingEmployment(ctx, time.Now())
	if err != nil || next.IsZero() || next.After(time.Now().Add(EmploymentWindow)) {
		t.Errorf("NextMissingEmployment = %s, %v; want the open window's end", next, err)
	}
	n, err := f.requests.RejectMissingEmployment(ctx, time.Now(), 10)
	if err != nil || n != 1 {
		t.Fatalf("RejectMissingEmployment = %d, %v; want 1", n, err)
	}
	checkStatuses(t, f.requests, map[*Request]Status{&missed: StatusRejected, &inWindow: StatusApproved, &held: StatusApproved})
	if auditCount(t, f.pool, audit.IdentityProofingRejected) != 1 {
		t.Error("want one rejected event")
	}
	deliveries, err := f.webhooks.Deliveries(ctx, f.orgID, f.customer.ID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != EventSessionFailed {
		t.Errorf("deliveries = %+v, %v; want one session.failed", deliveries, err)
	}
	if n, err := f.requests.RejectMissingEmployment(ctx, time.Now(), 10); err != nil || n != 0 {
		t.Errorf("a second run = %d, %v; want nothing left", n, err)
	}
}

// checkStatuses checks each request is stored with its status, and a rejected
// one with ErrorEmploymentNotProvided.
func checkStatuses(t *testing.T, requests *RequestStore, want map[*Request]Status) {
	t.Helper()
	for req, status := range want {
		got, err := requests.Get(context.Background(), req.OrganizationID, req.ID)
		if err != nil || got.Status != status {
			t.Errorf("request %s = %s, %v; want %s", req.SubjectEmail, got.Status, err, status)
		}
		if status == StatusRejected && got.ErrorCode != ErrorEmploymentNotProvided {
			t.Errorf("error code = %q, want %s", got.ErrorCode, ErrorEmploymentNotProvided)
		}
	}
}

// A current benefit, or other income, beside a job also puts the approved
// request in review.
func TestIncomeBesideJobOpensReview(t *testing.T) {
	for _, tc := range []struct {
		kind, employer, number, want string
	}{
		{"benefit", "WW-Uitkering van UWV", "810220350L02", ReviewCurrentBenefit},
		{"other_income", "PENSIOENFONDS", "111111111L01", ReviewCurrentOtherIncome},
	} {
		f := newSealedFixture(t, "Casino", EventSessionReviewOpened)
		ctx := context.Background()
		f.in.Employment = EmploymentRequired
		approved := approvedRequest(t, f.requests, f.in, "s1")

		emp := Employment{StatementDate: "2026-10-15", Employed: true, Relations: []EmploymentRelation{
			{Kind: "employment", Employer: "VOORBEELD B.V.", PayrollTaxNumber: "123456789L01", From: "2025-01-01", To: "2026-09-30"},
			{Kind: tc.kind, Employer: tc.employer, PayrollTaxNumber: tc.number, From: "2026-06-01", To: "2026-09-30"},
		}}
		if added, err := f.requests.AddEmployment(ctx, approved, emp); err != nil || !added {
			t.Fatalf("%s: AddEmployment = %v, %v; want added", tc.kind, added, err)
		}
		got, err := f.requests.Get(ctx, f.orgID, approved.ID)
		if err != nil || got.Status != StatusNeedsReview || got.ReviewReason != tc.want {
			t.Fatalf("%s: Get = %s (%q), %v; want needs_review for %s", tc.kind, got.Status, got.ReviewReason, err, tc.want)
		}
	}
}
