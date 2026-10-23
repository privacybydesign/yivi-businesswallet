package proofing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/employment"
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
		},
		{
			Employer: "VORIGE WERKGEVER B.V.", PayrollTaxNumber: "987654321L01", ContractForm: "oproepovereenkomst",
			From: time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
		},
	},
}

// fakeEmploymentChecker reads a file's bytes as the reason to refuse it, and
// accepts testStatement for any other.
type fakeEmploymentChecker struct{ holders []diploma.Person }

func (f *fakeEmploymentChecker) Check(_ context.Context, pdf []byte, holder diploma.Person) (employment.Outcome, error) {
	f.holders = append(f.holders, holder)
	switch reason := string(pdf); reason {
	case employment.ReasonNotAStatement, employment.ReasonSignature, employment.ReasonHolder, employment.ReasonStale:
		return employment.Outcome{Reason: reason}, nil
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
		Employer: "VOORBEELD B.V.", PayrollTaxNumber: "123456789L01", ContractForm: "schriftelijke arbeidsovereenkomst voor onbepaalde tijd",
		From: "2025-01-01", To: "2026-09-30",
	}
	got := e.requests.stored.SubjectEmployment
	if got == nil || got.StatementDate != "2026-10-15" || len(got.Relations) != 1 || got.Relations[0] != want {
		t.Errorf("kept %+v, want the statement of 2026-10-15 with only the current %+v", got, want)
	}
	if h := e.checker.holders[0]; h.GivenNames != "Anna" || h.Surname != "Jansen" || h.DateOfBirth != testBirthDate {
		t.Errorf("checked against %+v, want the identity IPS approved", h)
	}

	if _, err := e.upload(*e.requests.stored, "statement"); !errors.Is(err, ErrEmploymentClosed) {
		t.Errorf("a second statement = %v, want ErrEmploymentClosed", err)
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
		e.svc.employmentChecker = nil
		req := sendApprovedAddress(t, e.fixture, 0)
		if _, err := e.upload(req, "statement"); !errors.Is(err, ErrNoEmploymentChecker) {
			t.Errorf("AddEmployment = %v, want ErrNoEmploymentChecker", err)
		}
	})
}

func TestEmploymentUntil(t *testing.T) {
	completed := time.Now()
	req := Request{Employment: EmploymentRequired, Status: StatusApproved, CompletedAt: &completed}
	if until := employmentUntil(req); until == nil || !until.Equal(completed.Add(EmploymentWindow)) {
		t.Errorf("employmentUntil = %v, want the window's end", until)
	}
	req.SubjectEmployment = &Employment{}
	if until := employmentUntil(req); until != nil {
		t.Errorf("employmentUntil once kept = %v, want none", until)
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
