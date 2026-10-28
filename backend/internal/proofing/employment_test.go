package proofing

import (
	"context"
	"errors"
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
type fakeEmploymentChecker struct{ holders []diploma.Person }

func (f *fakeEmploymentChecker) Check(_ context.Context, pdf []byte, holder diploma.Person) (employment.Outcome, error) {
	f.holders = append(f.holders, holder)
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

// No current job sends the approved request to review, which the wallet
// decides itself: the engine approved the session and holds no review.
func TestNoJobGoesToReview(t *testing.T) {
	for _, want := range []Status{StatusApproved, StatusRejected} {
		checkNoJobReviewDecided(t, want)
	}
}

// checkNoJobReviewDecided keeps a statement without a job, then decides the
// review it opens to want, in the wallet alone.
func checkNoJobReviewDecided(t *testing.T, want Status) {
	t.Helper()
	approve := want == StatusApproved
	e := newEmploymentFixture(EmploymentRequired)
	req := sendApprovedAddress(t, e.fixture, time.Minute)
	verdict, err := e.upload(req, "no job")
	if err != nil || verdict.Employment == nil || verdict.Employment.Employed {
		t.Fatalf("a statement without a job = %+v, %v; want it kept, not employed", verdict, err)
	}
	if got := e.requests.stored; got.Status != StatusNeedsReview || got.ReviewReason != ReviewNoCurrentEmployment {
		t.Fatalf("request = %s (%q), want needs_review for no current employment", got.Status, got.ReviewReason)
	}
	if got := e.requests.stored.SubjectEmployment.Relations; len(got) != 1 || got[0].Kind != string(employment.KindBenefit) {
		t.Errorf("kept relations = %+v, want the WW benefit", got)
	}

	decided, err := e.svc.DecideReview(context.Background(), testOrg.ID, req.ID, "admin@example.org",
		ReviewInput{Approve: approve, Reason: "checked the player's income"})
	if err != nil {
		t.Fatalf("DecideReview to %s: %v", want, err)
	}
	if decided.Status != want || len(e.ips.decisions) != 0 {
		t.Errorf("decided %s with %d engine decisions, want %s decided in the wallet alone", decided.Status, len(e.ips.decisions), want)
	}
	if !approve && decided.ErrorCode != ErrorReviewRejected {
		t.Errorf("a rejection without a code = %q, want %s", decided.ErrorCode, ErrorReviewRejected)
	}
}

// In a paused org nobody can decide the review a statement opens, so it is
// rejected at once, as an engine review is.
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
	if got := e.requests.stored; got.Status != StatusRejected || got.ErrorCode != ErrorOrgPaused {
		t.Errorf("request = %s (%q), want rejected %s", got.Status, got.ErrorCode, ErrorOrgPaused)
	}
	// The wallet rejects it because the org is paused, not the uploader.
	if got := e.requests.actors; len(got) != 1 || got[0] != "" {
		t.Errorf("rejection actors = %q, want the wallet's (none)", got)
	}
}

// A current benefit beside a job sends the approved request to review too.
func TestBenefitGoesToReview(t *testing.T) {
	e := newEmploymentFixture(EmploymentRequired)
	req := sendApprovedAddress(t, e.fixture, time.Minute)
	verdict, err := e.upload(req, "benefit")
	if err != nil || verdict.Employment == nil || !verdict.Employment.Employed {
		t.Fatalf("a statement with a job and a benefit = %+v, %v; want it kept, employed", verdict, err)
	}
	if got := e.requests.stored; got.Status != StatusNeedsReview || got.ReviewReason != ReviewCurrentBenefit {
		t.Fatalf("request = %s (%q), want needs_review for a current benefit", got.Status, got.ReviewReason)
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

// The engine still reports the approval of a session the wallet put in
// review: reading it must not flip the request back.
func TestWalletReviewNotReconciled(t *testing.T) {
	e := newEmploymentFixture(EmploymentRequired)
	req := sendApprovedAddress(t, e.fixture, time.Minute)
	if _, err := e.upload(req, "no job"); err != nil {
		t.Fatal(err)
	}
	e.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: eidasSubstantial}
	got, err := e.svc.tryReconcile(context.Background(), requestTenant(*e.requests.stored), *e.requests.stored)
	if err != nil || got.Status != StatusNeedsReview || e.requests.stored.Status != StatusNeedsReview {
		t.Errorf("after a reconcile the request is %s (stored %s), %v; want it still in review", got.Status, e.requests.stored.Status, err)
	}
}

// No statement within the window rejects the session: a flow that requires
// one does not pass without it.
func TestMissingEmploymentRejects(t *testing.T) {
	for _, tc := range []struct {
		name         string
		completedAgo time.Duration
		kept         bool
		want         Status
	}{
		{"window still open", EmploymentWindow - time.Minute, false, StatusApproved},
		// An upload let in at the window's end may still be checked.
		{"within the grace", EmploymentWindow + employmentSweepGrace - time.Minute, false, StatusApproved},
		{"grace passed", missingEmploymentDue + time.Minute, false, StatusRejected},
		{"kept in time", missingEmploymentDue + time.Minute, true, StatusApproved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEmploymentFixture(EmploymentRequired)
			sendApprovedAddress(t, e.fixture, tc.completedAgo)
			if tc.kept {
				e.requests.stored.SubjectEmployment = &Employment{Employed: true}
			}
			if _, err := e.svc.ReconcileDue(context.Background()); err != nil {
				t.Fatalf("ReconcileDue: %v", err)
			}
			got := e.requests.stored
			if got.Status != tc.want {
				t.Errorf("status = %s, want %s", got.Status, tc.want)
			}
			if tc.want == StatusRejected && got.ErrorCode != ErrorEmploymentNotProvided {
				t.Errorf("error code = %q, want %s", got.ErrorCode, ErrorEmploymentNotProvided)
			}
		})
	}
}

// A rejection for a missing statement is the wallet's: the engine's approval,
// read again, leaves it.
func TestNoStatementNotReconciled(t *testing.T) {
	e := newEmploymentFixture(EmploymentRequired)
	sendApprovedAddress(t, e.fixture, missingEmploymentDue+time.Minute)
	if _, err := e.svc.ReconcileDue(context.Background()); err != nil {
		t.Fatal(err)
	}
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
// and is not asked again: the engine's approval then settles the request, or
// opens the wallet's review of the statement right after.
func TestEmploymentDuringEngineReview(t *testing.T) {
	for _, tc := range []struct {
		statement string
		want      Status
		reason    string
	}{
		{"statement", StatusApproved, ""},
		{"no job", StatusNeedsReview, ReviewNoCurrentEmployment},
	} {
		t.Run(tc.statement, func(t *testing.T) {
			e := newEmploymentFixture(EmploymentRequired)
			req := sendApprovedAddress(t, e.fixture, time.Minute)
			e.requests.stored.Status = StatusNeedsReview
			req.Status = StatusNeedsReview

			verdict, err := e.upload(req, tc.statement)
			if err != nil || verdict.Employment == nil {
				t.Fatalf("a statement in the engine's review = %+v, %v; want it kept", verdict, err)
			}
			if got := e.requests.stored; got.Status != StatusNeedsReview || got.ReviewReason != "" {
				t.Fatalf("request = %s (%q), want still the engine's review", got.Status, got.ReviewReason)
			}

			e.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: eidasSubstantial}
			got, err := e.svc.tryReconcile(context.Background(), requestTenant(*e.requests.stored), *e.requests.stored)
			if err != nil || got.Status != tc.want || got.ReviewReason != tc.reason {
				t.Fatalf("after the engine's approval = %s (%q), %v; want %s (%q)", got.Status, got.ReviewReason, err, tc.want, tc.reason)
			}

			// The window long past, nothing is missing.
			past := time.Now().Add(-missingEmploymentDue - time.Minute)
			e.requests.stored.CheckedAt = &past
			if _, err := e.svc.ReconcileDue(context.Background()); err != nil {
				t.Fatal(err)
			}
			if e.requests.stored.Status != tc.want {
				t.Errorf("after the sweep = %s, want %s", e.requests.stored.Status, tc.want)
			}
		})
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
