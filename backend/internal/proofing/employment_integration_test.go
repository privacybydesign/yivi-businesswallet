//go:build integration

package proofing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/employment"
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
	f := newSealedFixture(t, EventSessionEmploymentAdded)
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

	if err := requests.RecordEmploymentRejected(ctx, req, employment.ReasonHolder, ""); err != nil {
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
	deliveries, err := webhooks.Deliveries(ctx, req.OrganizationID)
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

// A kept statement without a current job opens its own review item, in the
// same transaction: the session stays approved, the item lists and counts as
// an employment review, and its decision settles only the step.
func TestNoJobOpensReview(t *testing.T) {
	f := newSealedFixture(t, EventSessionStepReviewed)
	ctx := context.Background()
	f.in.Employment = EmploymentRequired
	approved := approvedRequest(t, f.requests, f.in, "s1")

	emp := Employment{StatementDate: "2026-10-15", Relations: []EmploymentRelation{{
		Kind: string(employment.KindBenefit), Employer: "WW-Uitkering van UWV", PayrollTaxNumber: "810220350L02", From: "2026-06-01", To: "2026-09-30",
	}}}
	if added, err := f.requests.AddEmployment(ctx, approved, emp); err != nil || !added {
		t.Fatalf("AddEmployment = %v, %v; want added", added, err)
	}
	got, err := f.requests.Get(ctx, f.orgID, approved.ID)
	if err != nil || got.Status != StatusApproved || got.ReviewReason != ReviewNoCurrentEmployment || got.EmploymentReview != ReviewPending {
		t.Fatalf("Get = %s (%q, review %q), %v; want approved with the statement in review", got.Status, got.ReviewReason, got.EmploymentReview, err)
	}
	kind := ReviewKindEmployment
	if listed, err := f.requests.List(ctx, f.orgID, RequestFilter{ReviewKind: &kind}); err != nil || len(listed) != 1 {
		t.Errorf("List employment reviews = %d, %v; want the one", len(listed), err)
	}
	if counts, err := f.requests.ReviewCounts(ctx, f.orgID); err != nil || counts[ReviewKindEmployment] != 1 || counts[ReviewKindIdentity] != 0 {
		t.Errorf("ReviewCounts = %v, %v; want one employment review, no identity review", counts, err)
	}

	// The engine's approval, read again, changes nothing.
	if err := f.requests.RecordOutcome(ctx, got, "s1", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved}); err != nil {
		t.Fatal(err)
	}
	at, err := f.requests.DecideStepReview(ctx, got, ReviewKindEmployment, makeUser(t, f.pool, "rita@example.org"), ReviewRejected, "No job")
	if err != nil || at == nil {
		t.Fatalf("DecideStepReview = %v, %v; want decided", at, err)
	}
	decided, err := f.requests.Get(ctx, f.orgID, approved.ID)
	steps := decided.Steps(time.Now())
	if err != nil || decided.Status != StatusApproved || len(steps) != 1 || steps[0].State != StepRejected || steps[0].Code != ErrorEmploymentRejected {
		t.Errorf("after the decision = %s, steps %+v, %v; want approved, the step rejected", decided.Status, steps, err)
	}
	var step string
	if err := f.pool.QueryRow(ctx, `SELECT payload->'step'->>'state' FROM identity_proofing_webhook_deliveries
		WHERE organization_id = $1 AND event = $2`, f.orgID, EventSessionStepReviewed).Scan(&step); err != nil || step != string(ReviewRejected) {
		t.Errorf("session.step_reviewed step.state = %q, %v; want rejected", step, err)
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

	if _, err := f.pool.Exec(ctx, `UPDATE identity_proofing_requests
		SET completed_at = now() - make_interval(secs => $2), checked_at = now() - make_interval(secs => $2)
		WHERE id = $1`, req.ID, approvedAgo.Seconds()); err != nil {
		t.Fatal(err)
	}
	got, err := f.requests.Get(ctx, f.orgID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// An approved request without a Verzekeringsbericht past its window reads the
// step as not provided and stays approved; one in its window reads it open,
// and one that holds a statement done.
func TestMissingEmploymentNotProvidedStored(t *testing.T) {
	f := newSealedFixture(t, EventSessionFailed)
	ctx := context.Background()
	missed := approvedSince(t, f, "s1", EmploymentWindow+time.Minute)
	inWindow := approvedSince(t, f, "s2", time.Minute)
	held := approvedSince(t, f, "s3", EmploymentWindow+time.Minute)
	if added, err := f.requests.AddEmployment(ctx, held, Employment{StatementDate: "2026-10-15", Employed: true, Relations: []EmploymentRelation{}}); err != nil || !added {
		t.Fatalf("AddEmployment = %v, %v", added, err)
	}
	for req, want := range map[*Request]StepState{&missed: StepNotProvided, &inWindow: StepOpen, &held: StepDone} {
		got, err := f.requests.Get(ctx, f.orgID, req.ID)
		steps := got.Steps(time.Now())
		if err != nil || got.Status != StatusApproved || len(steps) != 1 || steps[0].State != want {
			t.Errorf("request %s = %s, steps %+v, %v; want approved, the step %s", req.SubjectEmail, got.Status, steps, err, want)
		}
	}
	if deliveries, err := f.webhooks.Deliveries(ctx, f.orgID); err != nil || len(deliveries) != 0 {
		t.Errorf("deliveries = %+v, %v; want no session.failed", deliveries, err)
	}
}

// A statement kept while the engine reviews the identity opens its own review
// at once; the engine's approval then settles the identity beside it.
func TestEmploymentKeptDuringEngineReview(t *testing.T) {
	f := newSealedFixture(t, EventSessionReviewOpened)
	ctx := context.Background()
	f.in.Employment = EmploymentRequired
	created, err := f.requests.Create(ctx, f.in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ok, err := f.requests.AttachSession(ctx, created, attachedSession("s1")); err != nil || !ok {
		t.Fatalf("AttachSession = %v, %v", ok, err)
	}
	req, err := f.requests.Get(ctx, f.orgID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.requests.RecordOutcome(ctx, req, "s1", StatusNeedsReview,
		proofingprovider.Result{Status: proofingprovider.StatusNeedsReview}); err != nil {
		t.Fatalf("RecordOutcome review: %v", err)
	}
	inReview, err := f.requests.Get(ctx, f.orgID, req.ID)
	if err != nil || inReview.CheckedAt == nil {
		t.Fatalf("in review: checked at %v, %v; want set", inReview.CheckedAt, err)
	}

	noJob := Employment{StatementDate: "2026-10-15", Relations: []EmploymentRelation{{
		Kind: string(employment.KindBenefit), Employer: "UWV", From: "2026-06-01", To: "2026-09-30",
	}}}
	if added, err := f.requests.AddEmployment(ctx, inReview, noJob); err != nil || !added {
		t.Fatalf("AddEmployment in review = %v, %v; want added", added, err)
	}
	if got, err := f.requests.Get(ctx, f.orgID, req.ID); err != nil || got.Status != StatusNeedsReview || got.EmploymentReview != ReviewPending {
		t.Fatalf("after the statement = %s, review %q, %v; want the engine's review, the statement's pending", got.Status, got.EmploymentReview, err)
	}

	if err := f.requests.RecordOutcome(ctx, inReview, "s1", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved}); err != nil {
		t.Fatalf("RecordOutcome approve: %v", err)
	}
	got, err := f.requests.Get(ctx, f.orgID, req.ID)
	if err != nil || got.Status != StatusApproved || got.EmploymentReview != ReviewPending {
		t.Fatalf("after the engine's approval = %s, review %q, %v; want approved, the statement's still pending", got.Status, got.EmploymentReview, err)
	}
	if !got.CheckedAt.Equal(*inReview.CheckedAt) {
		t.Errorf("checked at = %v, want it kept from the review's start %v", got.CheckedAt, inReview.CheckedAt)
	}
	// Only the engine's review opened one.
	deliveries, err := f.webhooks.Deliveries(ctx, f.orgID)
	if err != nil || len(deliveries) != 1 {
		t.Errorf("deliveries = %+v, %v; want one session.review_opened", deliveries, err)
	}
}

// A statement's review nobody decided in time lapses: the step reads rejected
// REVIEW_LAPSED, the session stays approved.
func TestEmploymentReviewLapses(t *testing.T) {
	f := newSealedFixture(t, EventSessionFailed)
	ctx := context.Background()
	f.in.Employment = EmploymentRequired
	approved := approvedRequest(t, f.requests, f.in, "s1")
	if added, err := f.requests.AddEmployment(ctx, approved, Employment{StatementDate: "2026-10-15", Relations: []EmploymentRelation{}}); err != nil || !added {
		t.Fatalf("AddEmployment = %v, %v; want added", added, err)
	}
	due, err := f.requests.NextReviewItemDue(ctx)
	if err != nil || due.IsZero() {
		t.Fatalf("NextReviewItemDue = %v, %v; want the item's", due, err)
	}
	if n, err := f.requests.LapseReviewItems(ctx, due, maxReconcilePerRound); err != nil || n != 1 {
		t.Fatalf("LapseReviewItems = %d, %v; want one", n, err)
	}
	got, err := f.requests.Get(ctx, f.orgID, approved.ID)
	steps := got.Steps(time.Now())
	if err != nil || got.Status != StatusApproved || got.EmploymentReview != ReviewLapsed || len(steps) != 1 ||
		steps[0].State != StepRejected || steps[0].Code != errorReviewLapsed {
		t.Fatalf("after the lapse = %s, review %q, steps %+v, %v; want approved, the step rejected %s",
			got.Status, got.EmploymentReview, steps, err, errorReviewLapsed)
	}
	if deliveries, err := f.webhooks.Deliveries(ctx, f.orgID); err != nil || len(deliveries) != 0 {
		t.Errorf("deliveries = %+v, %v; want no session.failed", deliveries, err)
	}
}

// A current benefit, or other income, beside a job also sends the statement
// to review.
func TestIncomeBesideJobReview(t *testing.T) {
	for _, tc := range []struct {
		kind, employer, number, want string
	}{
		{string(employment.KindBenefit), "WW-Uitkering van UWV", "810220350L02", ReviewCurrentBenefit},
		{string(employment.KindOtherIncome), "PENSIOENFONDS", "111111111L01", ReviewCurrentOtherIncome},
	} {
		f := newSealedFixture(t, EventSessionReviewOpened)
		ctx := context.Background()
		f.in.Employment = EmploymentRequired
		approved := approvedRequest(t, f.requests, f.in, "s1")

		emp := Employment{StatementDate: "2026-10-15", Employed: true, Relations: []EmploymentRelation{
			{Kind: string(employment.KindEmployment), Employer: "VOORBEELD B.V.", PayrollTaxNumber: "123456789L01", From: "2025-01-01", To: "2026-09-30"},
			{Kind: tc.kind, Employer: tc.employer, PayrollTaxNumber: tc.number, From: "2026-06-01", To: "2026-09-30"},
		}}
		if added, err := f.requests.AddEmployment(ctx, approved, emp); err != nil || !added {
			t.Fatalf("%s: AddEmployment = %v, %v; want added", tc.kind, added, err)
		}
		got, err := f.requests.Get(ctx, f.orgID, approved.ID)
		if err != nil || got.Status != StatusApproved || got.ReviewReason != tc.want || got.EmploymentReview != ReviewPending {
			t.Fatalf("%s: Get = %s (%q, review %q), %v; want approved, the statement in review for %s",
				tc.kind, got.Status, got.ReviewReason, got.EmploymentReview, err, tc.want)
		}
	}
}

// sealedFixture is an org whose webhook listens for one event, and a request
// for an external person still to be created.
type sealedFixture struct {
	pool     *pgxpool.Pool
	requests *RequestStore
	webhooks *WebhookStore
	orgID    uuid.UUID
	in       NewStoredRequest
}

func newSealedFixture(t *testing.T, event string) sealedFixture {
	t.Helper()
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	if _, _, err := webhooks.Save(context.Background(), orgID, "https://hooks.example.org/x", []string{event}); err != nil {
		t.Fatalf("Save webhook: %v", err)
	}
	return sealedFixture{
		pool:     pool,
		requests: NewRequestStore(pool, audit.NewDBRecorder(), cipher),
		webhooks: webhooks,
		orgID:    orgID,
		in:       newStoredRequest(orgID, sam, externalSubject("anna@example.org")),
	}
}
