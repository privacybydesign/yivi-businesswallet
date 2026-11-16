package proofing

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/employment"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

type fakeFlowEmployment struct{ modes map[string]EmploymentMode }

func (f *fakeFlowEmployment) Get(_ context.Context, _ uuid.UUID, flowID string) (EmploymentMode, error) {
	if mode, ok := f.modes[flowID]; ok {
		return mode, nil
	}
	return EmploymentOff, nil
}

func (f *fakeFlowEmployment) All(context.Context, uuid.UUID) (map[string]EmploymentMode, error) {
	return f.modes, nil
}

func (f *fakeFlowEmployment) Save(_ context.Context, _ uuid.UUID, flowID string, mode EmploymentMode) (EmploymentMode, error) {
	f.modes[flowID] = mode
	return mode, nil
}

// testStatement is what fakeEmploymentChecker reads off an accepted file.
var testStatement = &employment.Statement{
	Initials: []string{"A"}, Surname: "Jansen", Date: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC),
	Relations: []employment.Relation{
		{
			Employer: "VOORBEELD B.V.", PayrollTaxNumber: "123456789L01", ContractForm: "schriftelijke arbeidsovereenkomst voor onbepaalde tijd",
			From: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC),
			Worked: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), Kind: employment.KindEmployment,
		},
		{
			Employer: "VORIGE WERKGEVER B.V.", PayrollTaxNumber: "987654321L01", ContractForm: "oproepovereenkomst",
			From: time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
		},
	},
}

// noJobStatement is a statement whose only current relation is a WW benefit.
var noJobStatement = &employment.Statement{
	Initials: []string{"A"}, Surname: "Jansen", Date: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC),
	Relations: []employment.Relation{{
		Employer: "WW-Uitkering van UWV", PayrollTaxNumber: "810220350L02", Kind: employment.KindBenefit,
		From: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC),
	}},
}

// benefitStatement is a statement with a current job and a current WW
// benefit beside it.
var benefitStatement = &employment.Statement{
	Initials: []string{"A"}, Surname: "Jansen", Date: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC),
	Relations: append([]employment.Relation{testStatement.Relations[0]}, noJobStatement.Relations...),
}

// fakeEmploymentChecker reads a file's bytes as the reason to refuse it, and
// accepts noJobStatement for "no job", benefitStatement for "benefit" and
// testStatement for any other.
type fakeEmploymentChecker struct {
	holders []diploma.Person
	// down is a revocation responder that cannot be reached.
	down bool
}

func (f *fakeEmploymentChecker) Check(_ context.Context, pdf []byte, holder diploma.Person) (employment.Outcome, error) {
	f.holders = append(f.holders, holder)
	if f.down {
		return employment.Outcome{}, fmt.Errorf("employment: validate: %w", diploma.ErrRevocationUnavailable)
	}
	switch reason := string(pdf); reason {
	case employment.ReasonNotAStatement, employment.ReasonSignature, employment.ReasonHolder, employment.ReasonStale:
		return employment.Outcome{Reason: reason}, nil
	case "no job":
		return employment.Outcome{Statement: noJobStatement}, nil
	case "benefit":
		return employment.Outcome{Statement: benefitStatement}, nil
	}
	return employment.Outcome{Statement: testStatement}, nil
}

type employmentFixture struct {
	fixture
	checker *fakeEmploymentChecker
}

// newEmploymentFixture is a provisioned fixture whose appFlow asks for the
// Verzekeringsbericht as mode says.
func newEmploymentFixture(mode EmploymentMode) employmentFixture {
	f := newFixture()
	e := employmentFixture{fixture: f, checker: &fakeEmploymentChecker{}}
	f.svc.flowEmploymentSettings = &fakeFlowEmployment{modes: map[string]EmploymentMode{appFlow.ID: mode}}
	f.svc.SetEmploymentChecker(e.checker)
	return e
}

func (e employmentFixture) upload(req Request, content string) (EmploymentVerdict, error) {
	return e.svc.AddEmployment(context.Background(), testOrg.ID, req.ID, nil, DiplomaFile{Name: "Verzekeringsbericht.pdf", Bytes: []byte(content)})
}

func TestEmploymentFlowIsNotMailed(t *testing.T) {
	e := newEmploymentFixture(EmploymentRequired)
	_, err := e.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New(), Name: "Sam"},
		NewRequest{SubjectUserID: alex.UserID, FlowID: appFlow.ID})
	if !errors.Is(err, ErrEmploymentNeedsPage) {
		t.Fatalf("CreateRequest by mail = %v, want ErrEmploymentNeedsPage", err)
	}
	if len(e.ips.sessions) != 0 {
		t.Errorf("sessions created = %d, want none", len(e.ips.sessions))
	}
}

func TestEmploymentModeOnRequest(t *testing.T) {
	e := newEmploymentFixture(EmploymentRequired)
	sendApprovedAddress(t, e.fixture, 0)
	if got := e.requests.created[0].Employment; got != EmploymentRequired {
		t.Errorf("stored employment mode = %q, want required", got)
	}
}

func TestAddEmploymentKeepsOnce(t *testing.T) {
	e := newEmploymentFixture(EmploymentRequired)
	req := sendApprovedAddress(t, e.fixture, time.Minute)

	refused, err := e.upload(req, employment.ReasonHolder)
	if err != nil || refused.Reason != employment.ReasonHolder || refused.Employment != nil {
		t.Fatalf("a refused statement = %+v, %v; want holder_mismatch", refused, err)
	}
	if e.requests.stored.SubjectEmployment != nil || len(e.requests.employmentRejections) != 1 {
		t.Fatalf("a refused statement was kept, or its refusal not audited: %v", e.requests.employmentRejections)
	}

	kept, err := e.upload(req, "statement")
	if err != nil || kept.Employment == nil {
		t.Fatalf("AddEmployment = %+v, %v; want it kept", kept, err)
	}
	want := EmploymentRelation{
		Kind: string(employment.KindEmployment), Employer: "VOORBEELD B.V.", PayrollTaxNumber: "123456789L01", ContractForm: "schriftelijke arbeidsovereenkomst voor onbepaalde tijd",
		From: "2025-01-01", To: "2026-09-30",
	}
	got := e.requests.stored.SubjectEmployment
	if got == nil || got.StatementDate != "2026-10-15" || !got.Employed || len(got.Relations) != 1 || got.Relations[0] != want {
		t.Errorf("kept %+v, want the statement of 2026-10-15 with only the current %+v", got, want)
	}
	if h := e.checker.holders[0]; h.GivenNames != "Anna" || h.Surname != "Jansen" || h.DateOfBirth != testBirthDate {
		t.Errorf("checked against %+v, want the identity IPS approved", h)
	}
	if e.requests.stored.Status != StatusApproved || e.requests.stored.ReviewReason != "" {
		t.Errorf("a statement with a current job left the request %s (%q), want it approved", e.requests.stored.Status, e.requests.stored.ReviewReason)
	}

	if _, err := e.upload(*e.requests.stored, "statement"); !errors.Is(err, ErrEmploymentClosed) {
		t.Errorf("a second statement = %v, want ErrEmploymentClosed", err)
	}
}

// No current job opens the statement's own review: the session stays
// approved, and the reviewer's decision settles only the step.
func TestNoJobGoesToReview(t *testing.T) {
	for _, confirm := range []bool{true, false} {
		checkNoJobReviewDecided(t, confirm)
	}
}

// checkNoJobReviewDecided keeps a statement without a job, then decides the
// review item it opens, confirming it or not.
func checkNoJobReviewDecided(t *testing.T, confirm bool) {
	t.Helper()
	e := newEmploymentFixture(EmploymentRequired)
	req := sendApprovedAddress(t, e.fixture, time.Minute)
	verdict, err := e.upload(req, "no job")
	if err != nil || verdict.Employment == nil || verdict.Employment.Employed {
		t.Fatalf("a statement without a job = %+v, %v; want it kept, not employed", verdict, err)
	}
	got := e.requests.stored
	if got.Status != StatusApproved || got.ReviewReason != ReviewNoCurrentEmployment || got.EmploymentReview != ReviewPending {
		t.Fatalf("request = %s (%q, review %q), want approved with the statement in review", got.Status, got.ReviewReason, got.EmploymentReview)
	}
	if steps := got.Steps(time.Now()); len(steps) != 1 || steps[0].State != StepInReview {
		t.Errorf("steps = %+v, want the employment step in review", steps)
	}
	if rel := got.SubjectEmployment.Relations; len(rel) != 1 || rel[0].Kind != string(employment.KindBenefit) {
		t.Errorf("kept relations = %+v, want the WW benefit", rel)
	}

	decided, err := e.svc.DecideReviewItem(context.Background(), testOrg.ID, req.ID, ReviewKindEmployment, "admin@example.org",
		ReviewItemInput{Confirm: confirm, Reason: "checked the player's income", ReviewerID: uuid.New(), Scope: ReviewScopeAll})
	if err != nil {
		t.Fatalf("DecideReviewItem confirm=%v: %v", confirm, err)
	}
	wantStep, wantCode := StepDone, ""
	if !confirm {
		wantStep, wantCode = StepRejected, ErrorEmploymentRejected
	}
	steps := decided.Steps(time.Now())
	if decided.Status != StatusApproved || len(e.ips.decisions) != 0 || len(steps) != 1 ||
		steps[0].State != wantStep || steps[0].Code != wantCode {
		t.Errorf("decided %s, steps %+v, %d engine decisions; want approved, the step %s %s, none to the engine",
			decided.Status, steps, len(e.ips.decisions), wantStep, wantCode)
	}
}

// In a paused org nobody can decide the review a statement opens, so it is
// rejected at once; the session keeps its outcome.
func TestNoJobReviewRejectedPaused(t *testing.T) {
	e := newEmploymentFixture(EmploymentRequired)
	pauses := withPauses(e.fixture)
	req := sendApprovedAddress(t, e.fixture, time.Minute)
	now := time.Now()
	pauses.byOrg[testOrg.ID] = OrgPause{OrganizationID: testOrg.ID, PlatformPausedAt: &now}

	byLink := audit.ContextWithActor(context.Background(), hostedSubjectActor)
	verdict, err := e.svc.AddEmployment(byLink, testOrg.ID, req.ID, nil, DiplomaFile{Name: "Verzekeringsbericht.pdf", Bytes: []byte("no job")})
	if err != nil || verdict.Employment == nil {
		t.Fatalf("a statement without a job = %+v, %v; want it kept", verdict, err)
	}
	if got := e.requests.stored; got.Status != StatusApproved || got.EmploymentReview != ReviewRejected {
		t.Errorf("request = %s, review %q; want approved with the statement's review rejected", got.Status, got.EmploymentReview)
	}
}

// A current benefit beside a job sends the statement to review too.
func TestBenefitGoesToReview(t *testing.T) {
	e := newEmploymentFixture(EmploymentRequired)
	req := sendApprovedAddress(t, e.fixture, time.Minute)
	verdict, err := e.upload(req, "benefit")
	if err != nil || verdict.Employment == nil || !verdict.Employment.Employed {
		t.Fatalf("a statement with a job and a benefit = %+v, %v; want it kept, employed", verdict, err)
	}
	if got := e.requests.stored; got.Status != StatusApproved || got.ReviewReason != ReviewCurrentBenefit || got.EmploymentReview != ReviewPending {
		t.Fatalf("request = %s (%q, review %q), want approved with the statement in review for a current benefit",
			got.Status, got.ReviewReason, got.EmploymentReview)
	}
}

// A flow that reviews every statement sends one with a current job to a
// reviewer too, with no reason of its own.
func TestAlwaysReviewStatementReviewed(t *testing.T) {
	e := newEmploymentFixture(EmploymentAlwaysReview)
	req := sendApprovedAddress(t, e.fixture, time.Minute)
	verdict, err := e.upload(req, "statement")
	if err != nil || verdict.Employment == nil || !verdict.Employment.Employed {
		t.Fatalf("a statement with a job = %+v, %v; want it kept, employed", verdict, err)
	}
	got := e.requests.stored
	if got.Status != StatusApproved || got.ReviewReason != "" || got.EmploymentReview != ReviewPending {
		t.Fatalf("request = %s (%q, review %q), want approved with the statement in review, no reason",
			got.Status, got.ReviewReason, got.EmploymentReview)
	}
	if steps := got.Steps(time.Now()); len(steps) != 1 || steps[0].State != StepInReview {
		t.Errorf("steps = %+v, want the employment step in review", steps)
	}
}

func TestEmploymentReviewReason(t *testing.T) {
	job := EmploymentRelation{Kind: string(employment.KindEmployment)}
	benefit := EmploymentRelation{Kind: string(employment.KindBenefit)}
	other := EmploymentRelation{Kind: string(employment.KindOtherIncome)}
	for _, tc := range []struct {
		name string
		emp  Employment
		want string
	}{
		{"job", Employment{Employed: true, Relations: []EmploymentRelation{job}}, ""},
		{"job and benefit", Employment{Employed: true, Relations: []EmploymentRelation{job, benefit}}, ReviewCurrentBenefit},
		{"job and other income", Employment{Employed: true, Relations: []EmploymentRelation{job, other}}, ReviewCurrentOtherIncome},
		{"job, benefit and other income", Employment{Employed: true, Relations: []EmploymentRelation{job, other, benefit}}, ReviewCurrentBenefit},
		{"benefit only", Employment{Relations: []EmploymentRelation{benefit}}, ReviewNoCurrentEmployment},
		{"other income only", Employment{Relations: []EmploymentRelation{other}}, ReviewNoCurrentEmployment},
		{"nothing", Employment{}, ReviewNoCurrentEmployment},
	} {
		if got := tc.emp.reviewReason(); got != tc.want {
			t.Errorf("%s: reviewReason = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The statement's review is no review of the session: deciding the identity
// is refused, deciding the item leaves the session approved.
func TestEmploymentReviewIsTheStepsOwn(t *testing.T) {
	e := newEmploymentFixture(EmploymentRequired)
	req := sendApprovedAddress(t, e.fixture, time.Minute)
	if _, err := e.upload(req, "no job"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	in := ReviewItemInput{Confirm: true, Reason: testReason, ReviewerID: uuid.New(), Scope: ReviewScopeAll}
	if _, err := e.svc.DecideReviewItem(ctx, testOrg.ID, req.ID, ReviewKindIdentity, "rita", in); !errors.Is(err, ErrNotUnderReview) {
		t.Fatalf("DecideReviewItem identity = %v; want ErrNotUnderReview", err)
	}
	got, err := e.svc.DecideReviewItem(ctx, testOrg.ID, req.ID, ReviewKindEmployment, "rita", in)
	if err != nil || got.Status != StatusApproved || got.EmploymentReview != ReviewConfirmed {
		t.Errorf("DecideReviewItem employment = %s, review %q, %v; want approved, confirmed", got.Status, got.EmploymentReview, err)
	}
	if _, err := e.svc.DecideReviewItem(ctx, testOrg.ID, req.ID, ReviewKindEmployment, "rita", in); !errors.Is(err, ErrNotUnderReview) {
		t.Errorf("DecideReviewItem again = %v; want ErrNotUnderReview", err)
	}
}

// No statement within the window leaves the step not provided: the session
// keeps the identity's approval.
func TestMissingEmploymentNotProvided(t *testing.T) {
	for _, tc := range []struct {
		name         string
		completedAgo time.Duration
		kept         bool
		want         StepState
	}{
		{"window still open", EmploymentWindow - time.Minute, false, StepOpen},
		{"window passed", EmploymentWindow + time.Minute, false, StepNotProvided},
		{"kept in time", EmploymentWindow + time.Minute, true, StepDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEmploymentFixture(EmploymentRequired)
			sendApprovedAddress(t, e.fixture, tc.completedAgo)
			if tc.kept {
				added := time.Now().Add(-tc.completedAgo)
				e.requests.stored.SubjectEmployment, e.requests.stored.EmploymentAddedAt = &Employment{Employed: true}, &added
			}
			if _, err := e.svc.ReconcileDue(context.Background()); err != nil {
				t.Fatalf("ReconcileDue: %v", err)
			}
			got := e.requests.stored
			steps := got.Steps(time.Now())
			if got.Status != StatusApproved || len(steps) != 1 || steps[0].State != tc.want {
				t.Fatalf("status %s, steps %+v; want approved with the step %s", got.Status, steps, tc.want)
			}
			if tc.want == StepNotProvided && steps[0].Code != ErrorEmploymentNotProvided {
				t.Errorf("code = %q, want %s", steps[0].Code, ErrorEmploymentNotProvided)
			}
		})
	}
}

// A session rejected for a missing statement before the step had its own
// outcome keeps that rejection: the engine's approval, read again, leaves it.
func TestNoStatementRejectionNotReconciled(t *testing.T) {
	e := newEmploymentFixture(EmploymentRequired)
	sendApprovedAddress(t, e.fixture, EmploymentWindow+time.Minute)
	e.requests.stored.Status, e.requests.stored.ErrorCode = StatusRejected, ErrorEmploymentNotProvided
	e.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: eidasSubstantial}
	got, err := e.svc.tryReconcile(context.Background(), requestTenant(*e.requests.stored), *e.requests.stored)
	if err != nil || got.Status != StatusRejected {
		t.Errorf("after a reconcile = %s, %v; want still rejected", got.Status, err)
	}
}

func TestAddEmploymentRefused(t *testing.T) {
	cases := []struct {
		name         string
		mode         EmploymentMode
		status       Status
		completedAgo time.Duration
		want         error
	}{
		{"flow asks none", EmploymentOff, StatusApproved, 0, ErrEmploymentNotAsked},
		{"identity rejected", EmploymentRequired, StatusRejected, 0, ErrEmploymentClosed},
		{"window passed", EmploymentRequired, StatusApproved, EmploymentWindow + time.Minute, ErrEmploymentClosed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEmploymentFixture(tc.mode)
			sendApprovedAddress(t, e.fixture, tc.completedAgo)
			e.requests.stored.Status = tc.status
			if _, err := e.upload(*e.requests.stored, "statement"); !errors.Is(err, tc.want) {
				t.Errorf("AddEmployment = %v, want %v", err, tc.want)
			}
		})
	}

	t.Run("no checker", func(t *testing.T) {
		e := newEmploymentFixture(EmploymentRequired)
		req := sendApprovedAddress(t, e.fixture, 0)
		e.svc.employmentChecker = nil
		if _, err := e.upload(req, "statement"); !errors.Is(err, ErrNoEmploymentChecker) {
			t.Errorf("AddEmployment = %v, want ErrNoEmploymentChecker", err)
		}
	})
}

func TestEmploymentUntil(t *testing.T) {
	checked := time.Now()
	req := Request{Employment: EmploymentRequired, Status: StatusApproved, CheckedAt: &checked}
	if until := employmentUntil(req); until == nil || !until.Equal(checked.Add(EmploymentWindow)) {
		t.Errorf("employmentUntil = %v, want the window's end", until)
	}
	// Open while the engine reviews the identity, from when the check
	// finished: a reviewer's approval later moves nothing.
	req.Status = StatusNeedsReview
	if until := employmentUntil(req); until == nil || !until.Equal(checked.Add(EmploymentWindow)) {
		t.Errorf("employmentUntil in review = %v, want the window's end", until)
	}
	req.Status = StatusRejected
	if until := employmentUntil(req); until != nil {
		t.Errorf("employmentUntil once rejected = %v, want none", until)
	}
	req.Status = StatusApproved
	req.SubjectEmployment = &Employment{}
	if until := employmentUntil(req); until != nil {
		t.Errorf("employmentUntil once kept = %v, want none", until)
	}
}

// A flow set to required before the checker was switched off sends nothing:
// its subject could never upload, and would be rejected after the window.
func TestEmploymentRequestNeedsChecker(t *testing.T) {
	e := newEmploymentFixture(EmploymentRequired)
	e.svc.employmentChecker = nil
	_, err := e.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New(), Name: "Sam"},
		NewRequest{SubjectUserID: alex.UserID, FlowID: appFlow.ID, Channel: ChannelOnScreen})
	if !errors.Is(err, ErrNoEmploymentChecker) {
		t.Fatalf("CreateRequest without a checker = %v, want ErrNoEmploymentChecker", err)
	}
	if len(e.ips.sessions) != 0 {
		t.Errorf("sessions created = %d, want none", len(e.ips.sessions))
	}
}

// The subject gives the statement while the engine reviews their identity,
// and is not asked again: the statement's own review opens at once, and the
// engine's approval settles the identity beside it.
func TestEmploymentDuringEngineReview(t *testing.T) {
	for _, tc := range []struct {
		statement string
		review    ReviewState
	}{
		{"statement", ""},
		{"no job", ReviewPending},
	} {
		t.Run(tc.statement, func(t *testing.T) { checkEmploymentDuringEngineReview(t, tc.statement, tc.review) })
	}
}

// checkEmploymentDuringEngineReview keeps statement while the engine reviews
// the identity, then has the engine approve: the request is approved, with
// the statement's review at review.
func checkEmploymentDuringEngineReview(t *testing.T, statement string, review ReviewState) {
	t.Helper()
	e := newEmploymentFixture(EmploymentRequired)
	req := sendApprovedAddress(t, e.fixture, time.Minute)
	e.requests.stored.Status = StatusNeedsReview
	req.Status = StatusNeedsReview

	verdict, err := e.upload(req, statement)
	if err != nil || verdict.Employment == nil {
		t.Fatalf("a statement in the engine's review = %+v, %v; want it kept", verdict, err)
	}
	if got := e.requests.stored; got.Status != StatusNeedsReview || got.EmploymentReview != review {
		t.Fatalf("request = %s, review %q; want still the engine's review, the statement's %q", got.Status, got.EmploymentReview, review)
	}

	e.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: eidasSubstantial}
	got, err := e.svc.tryReconcile(context.Background(), requestTenant(*e.requests.stored), *e.requests.stored)
	if err != nil || got.Status != StatusApproved || got.EmploymentReview != review {
		t.Fatalf("after the engine's approval = %s, review %q, %v; want approved, %q", got.Status, got.EmploymentReview, err, review)
	}
}

// Requiring the statement needs a flow that reads the name and date of birth:
// its holder is matched against them.
func TestEmploymentNeedsIdentity(t *testing.T) {
	e := newEmploymentFixture(EmploymentOff)
	if _, err := e.svc.SaveFlowEmployment(context.Background(), testOrg, appFlow.ID, EmploymentRequired); !errors.Is(err, ErrFlowNoIdentity) {
		t.Errorf("SaveFlowEmployment on a flow without dg1 = %v, want ErrFlowNoIdentity", err)
	}
	if _, err := e.svc.SaveFlowEmployment(context.Background(), testOrg, chipFlow.ID, EmploymentRequired); err != nil {
		t.Errorf("SaveFlowEmployment on a flow reading the document = %v", err)
	}
	if _, err := e.svc.SaveFlowEmployment(context.Background(), testOrg, chipFlow.ID, "sometimes"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("an unknown mode = %v, want ErrInvalidInput", err)
	}
}

// A deployment that checks no statement (EMPLOYMENT_VALIDATOR_PROVIDER=off)
// lets no flow require one, and can still switch the step off.
func TestEmploymentRequiresChecker(t *testing.T) {
	e := newEmploymentFixture(EmploymentOff)
	e.svc.employmentChecker = nil
	if _, err := e.svc.SaveFlowEmployment(context.Background(), testOrg, chipFlow.ID, EmploymentRequired); !errors.Is(err, ErrNoEmploymentChecker) {
		t.Errorf("SaveFlowEmployment required without a checker = %v, want ErrNoEmploymentChecker", err)
	}
	if _, err := e.svc.SaveFlowEmployment(context.Background(), testOrg, chipFlow.ID, EmploymentOff); err != nil {
		t.Errorf("SaveFlowEmployment off without a checker = %v, want saved", err)
	}
}
