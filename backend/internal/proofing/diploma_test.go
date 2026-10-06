package proofing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma"
)

// testBirthDate is the birth date the fake IPS reads off every document.
const testBirthDate = "1990-04-12"

type fakeFlowDiplomas struct{ modes map[string]DiplomaMode }

func (f *fakeFlowDiplomas) Get(_ context.Context, _ uuid.UUID, flowID string) (DiplomaMode, error) {
	if mode, ok := f.modes[flowID]; ok {
		return mode, nil
	}
	return DiplomasOff, nil
}

func (f *fakeFlowDiplomas) All(context.Context, uuid.UUID) (map[string]DiplomaMode, error) {
	return f.modes, nil
}

func (f *fakeFlowDiplomas) Save(_ context.Context, _ uuid.UUID, flowID string, mode DiplomaMode) (DiplomaMode, error) {
	f.modes[flowID] = mode
	return mode, nil
}

type fakeDiplomas struct {
	held     map[uuid.UUID][]Diploma
	rejected []string
}

func (f *fakeDiplomas) List(_ context.Context, ids []uuid.UUID) (map[uuid.UUID][]Diploma, error) {
	out := map[uuid.UUID][]Diploma{}
	for _, id := range ids {
		out[id] = f.held[id]
	}
	return out, nil
}

func (f *fakeDiplomas) Add(_ context.Context, req Request, d Diploma) (Diploma, bool, error) {
	for _, held := range f.held[req.ID] {
		if held.DocumentNumber == d.DocumentNumber {
			return Diploma{}, false, nil
		}
	}
	d.ID = uuid.New()
	f.held[req.ID] = append(f.held[req.ID], d)
	return d, true, nil
}

func (f *fakeDiplomas) RecordRejected(_ context.Context, _ Request, reason, _ string) error {
	f.rejected = append(f.rejected, reason)
	return nil
}

// fakeChecker reads a file's bytes as the holder's full name, and accepts the
// extract when it names the holder.
type fakeChecker struct{ holders []diploma.Person }

func (f *fakeChecker) Check(_ context.Context, pdf []byte, holder diploma.Person) (diploma.Outcome, error) {
	f.holders = append(f.holders, holder)
	if string(pdf) == "not a pdf" {
		return diploma.Outcome{Reason: diploma.ReasonNotADiploma}, nil
	}
	doc := &diploma.Document{
		DocumentType: "Diploma", Qualification: "HBO Bachelor Verpleegkunde", FullName: string(pdf),
		Institution: "Hogeschool Utrecht", PlaceOfIssue: "Utrecht", DateAwarded: time.Date(2015, 7, 1, 0, 0, 0, 0, time.UTC),
		NLQFLevel: "6", EQFLevel: "6", DocumentNumber: string(pdf),
	}
	out := diploma.Outcome{Document: doc}
	if !diploma.MatchFullName(doc.FullName, holder.DateOfBirth, holder).Matched {
		out.Reason = diploma.ReasonHolder
	}
	return out, nil
}

type diplomaFixture struct {
	fixture
	diplomas *fakeDiplomas
	checker  *fakeChecker
}

// newDiplomaFixture is a provisioned fixture whose appFlow asks for diplomas
// as mode says.
func newDiplomaFixture(mode DiplomaMode) diplomaFixture {
	f := newFixture()
	d := diplomaFixture{fixture: f, diplomas: &fakeDiplomas{held: map[uuid.UUID][]Diploma{}}, checker: &fakeChecker{}}
	f.svc.flowDiplomaSettings = &fakeFlowDiplomas{modes: map[string]DiplomaMode{appFlow.ID: mode}}
	f.svc.diplomas = d.diplomas
	f.svc.SetDiplomaChecker(d.checker)
	return d
}

// sendApproved sends appFlow on screen and has IPS approve it completedAgo ago.
func (d diplomaFixture) sendApproved(t *testing.T, completedAgo time.Duration) Request {
	t.Helper()
	if _, err := d.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New(), Name: "Sam"},
		NewRequest{SubjectUserID: alex.UserID, FlowID: appFlow.ID, Channel: ChannelOnScreen}); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	completed := time.Now().Add(-completedAgo)
	d.requests.stored.Status, d.requests.stored.CompletedAt = StatusApproved, &completed
	return *d.requests.stored
}

func (d diplomaFixture) upload(req Request, files ...string) ([]DiplomaVerdict, error) {
	in := make([]DiplomaFile, 0, len(files))
	for _, f := range files {
		in = append(in, DiplomaFile{Name: f + ".pdf", Bytes: []byte(f)})
	}
	return d.svc.AddDiplomas(context.Background(), testOrg.ID, req.ID, nil, in)
}

func TestDiplomaFlowIsNotMailed(t *testing.T) {
	d := newDiplomaFixture(DiplomasRequired)
	_, err := d.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New(), Name: "Sam"},
		NewRequest{SubjectUserID: alex.UserID, FlowID: appFlow.ID})
	if !errors.Is(err, ErrDiplomasNeedPage) {
		t.Fatalf("CreateRequest by mail = %v, want ErrDiplomasNeedPage", err)
	}
	if len(d.ips.sessions) != 0 {
		t.Errorf("sessions created = %d, want none", len(d.ips.sessions))
	}
}

func TestDiplomaModeKeptOnRequest(t *testing.T) {
	d := newDiplomaFixture(DiplomasRequired)
	req := d.sendApproved(t, 0)
	if req.Diplomas != DiplomasRequired || d.requests.created[0].Diplomas != DiplomasRequired {
		t.Errorf("request diplomas = %q, stored %q; want required", req.Diplomas, d.requests.created[0].Diplomas)
	}
}

func TestAddDiplomasHoldersOnly(t *testing.T) {
	d := newDiplomaFixture(DiplomasRequired)
	req := d.sendApproved(t, time.Minute)
	verdicts, err := d.upload(req, "Anna Maria Jansen", "Piet de Vries", "not a pdf", "Anna Maria Jansen")
	if err != nil {
		t.Fatalf("AddDiplomas: %v", err)
	}
	want := []string{"", diploma.ReasonHolder, diploma.ReasonNotADiploma, DiplomaReasonDuplicate}
	for i, v := range verdicts {
		if v.Reason != want[i] || (v.Reason == "") != (v.Diploma != nil) {
			t.Errorf("verdict %d = %+v, want reason %q", i, v, want[i])
		}
	}
	if held := d.diplomas.held[req.ID]; len(held) != 1 || held[0].Qualification != "HBO Bachelor Verpleegkunde" {
		t.Errorf("held = %+v, want the one extract naming the holder", held)
	}
	if len(d.diplomas.rejected) != 3 {
		t.Errorf("rejections audited = %v, want three", d.diplomas.rejected)
	}
	if h := d.checker.holders[0]; h.GivenNames != "Anna" || h.Surname != "Jansen" || h.DateOfBirth != testBirthDate {
		t.Errorf("checked against %+v, want the identity IPS approved", h)
	}
}

func TestAddDiplomasRefused(t *testing.T) {
	cases := []struct {
		name         string
		mode         DiplomaMode
		status       Status
		completedAgo time.Duration
		want         error
	}{
		{"flow asks none", DiplomasOff, StatusApproved, 0, ErrDiplomasNotAsked},
		{"identity rejected", DiplomasRequired, StatusRejected, 0, ErrDiplomasClosed},
		{"window passed", DiplomasRequired, StatusApproved, DiplomaUploadWindow + time.Minute, ErrDiplomasClosed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newDiplomaFixture(tc.mode)
			req := d.sendApproved(t, tc.completedAgo)
			d.requests.stored.Status = tc.status
			req.Status = tc.status
			if _, err := d.upload(req, "Anna Jansen"); !errors.Is(err, tc.want) {
				t.Errorf("AddDiplomas = %v, want %v", err, tc.want)
			}
			if len(d.checker.holders) != 0 {
				t.Error("a refused upload was checked")
			}
		})
	}
}

func TestAddDiplomasCapsTheCount(t *testing.T) {
	d := newDiplomaFixture(DiplomasRequired)
	req := d.sendApproved(t, 0)
	files := make([]string, MaxDiplomasPerRequest+1)
	for i := range files {
		files[i] = "Anna Jansen"
	}
	if _, err := d.upload(req, files...); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("AddDiplomas of %d = %v, want ErrInvalidInput", len(files), err)
	}
}
