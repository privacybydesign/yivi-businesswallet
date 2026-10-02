package proofing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// sendForPerson sends initech's subject a request for one known person.
func (f fixture) sendForPerson(t *testing.T, name, birthDate string) Sent {
	t.Helper()
	sent, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New(), Name: "Sam"},
		NewRequest{
			CustomerID: &initech.ID, SubjectEmail: "anna@example.org", SubjectName: name,
			SubjectBirthDate: birthDate, FlowID: chipFlow.ID,
		})
	if err != nil {
		t.Fatalf("CreateRequest for a person: %v", err)
	}
	return sent
}

// A request for one known person is approved only for that person: the fake
// IPS reads Anna Jansen, born testBirthDate, off every document.
func TestExpectedSubjectIsMatched(t *testing.T) {
	for name, tc := range map[string]struct {
		subject, birthDate string
		want               Status
		wantCode           string
	}{
		"same person":           {"Anna Jansen", testBirthDate, StatusApproved, ""},
		"written differently":   {"  anna   JANSEN ", testBirthDate, StatusApproved, ""},
		"another name":          {"Dibran Mulder", testBirthDate, StatusRejected, ErrorIdentityMismatch},
		"another date of birth": {"Anna Jansen", "1991-04-12", StatusRejected, ErrorIdentityMismatch},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture()
			sent := f.sendForPerson(t, tc.subject, tc.birthDate)
			if !sent.Request.ExpectsSubject {
				t.Fatal("the request does not expect its subject")
			}
			f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}
			req := f.reconcile(t)
			if req.Status != tc.want || req.ErrorCode != tc.wantCode {
				t.Errorf("status = %s, code = %q; want %s, %q", req.Status, req.ErrorCode, tc.want, tc.wantCode)
			}
			if req.expectedBirthDate != "" {
				t.Error("the expected birth date is still held once decided")
			}
			if tc.want == StatusRejected && f.requests.names[0] != "" {
				t.Errorf("a mismatch kept the proofed name %q", f.requests.names[0])
			}
		})
	}
}

// A request without a birth date proofs whoever takes part: the name is only a
// label, and the identity is never read to match it.
func TestNameAloneIsNotMatched(t *testing.T) {
	f := newFixture()
	f.sendForCustomer(t, "anna@example.org", "Dibran Mulder")
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}
	if req := f.reconcile(t); req.Status != StatusApproved || req.ExpectsSubject {
		t.Errorf("status = %s, expects subject = %v; want approved for anyone", req.Status, req.ExpectsSubject)
	}
}

// The identity can only be matched once IPS approved; a failed read leaves
// the request undecided for the next reconcile.
func TestExpectedSubjectIdentityReadFails(t *testing.T) {
	f := newFixture()
	f.sendForPerson(t, "Anna Jansen", testBirthDate)
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved}
	f.ips.resultErr = errors.New("unreachable")
	if req := f.reconcile(t); req.Status.Settled() {
		t.Errorf("status = %s, want undecided while the identity cannot be read", req.Status)
	}
	if len(f.requests.outcomes) != 0 {
		t.Errorf("outcomes recorded = %v, want none", f.requests.outcomes)
	}
}

func TestExpectedSubjectValidates(t *testing.T) {
	future := time.Now().AddDate(1, 0, 0).Format(birthDateLayout)
	for name, tc := range map[string]NewRequest{
		"no name":          {CustomerID: &initech.ID, SubjectEmail: "a@example.org", SubjectBirthDate: testBirthDate, FlowID: chipFlow.ID},
		"not a date":       {CustomerID: &initech.ID, SubjectEmail: "a@example.org", SubjectName: "Anna Jansen", SubjectBirthDate: "12-04-1990", FlowID: chipFlow.ID},
		"in the future":    {CustomerID: &initech.ID, SubjectEmail: "a@example.org", SubjectName: "Anna Jansen", SubjectBirthDate: future, FlowID: chipFlow.ID},
		"a member":         {SubjectUserID: alex.UserID, SubjectBirthDate: testBirthDate, FlowID: appFlow.ID},
		"no document data": {CustomerID: &initech.ID, SubjectEmail: "a@example.org", SubjectName: "Anna Jansen", SubjectBirthDate: testBirthDate, FlowID: "f-nodata"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture()
			noData := testFlow("f-nodata", "Chip, no document data", []string{"document_capture", "nfc_read"}, "native")
			noData.RequestedAttributes = []string{"dg2", "chip_checks"}
			f.ips.flows = append(f.ips.flows, noData)
			c := f.customers.byID[initech.ID]
			c.Flows.FlowIDs = append(c.Flows.FlowIDs, noData.ID)
			f.customers.byID[initech.ID] = c
			_, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New(), Name: "Sam"}, tc)
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput", err)
			}
			if len(f.requests.created) != 0 {
				t.Error("a refused request was stored")
			}
		})
	}
}

func TestReadsIdentity(t *testing.T) {
	for name, tc := range map[string]struct {
		flow proofingprovider.Flow
		want bool
	}{
		"steps scan the document":  {testFlow("a", "a", []string{"document_capture", "nfc_read"}, "native"), true},
		"steps only read the chip": {testFlow("b", "b", []string{"nfc_read"}, "native"), false},
		"requests the document data": {
			proofingprovider.Flow{FlowSpec: proofingprovider.FlowSpec{Steps: []string{"document_capture"}, RequestedAttributes: []string{"dg1"}}}, true,
		},
		"leaves the document data out": {
			proofingprovider.Flow{FlowSpec: proofingprovider.FlowSpec{Steps: []string{"document_capture"}, RequestedAttributes: []string{"dg2"}}}, false,
		},
	} {
		if got := ReadsIdentity(tc.flow); got != tc.want {
			t.Errorf("%s: ReadsIdentity = %v, want %v", name, got, tc.want)
		}
	}
}

// A request the wallet rejected for someone other than the expected person
// reads as that rejection, without the identity the engine approved.
func TestMismatchResultHidesTheIdentity(t *testing.T) {
	f := newFixture()
	sent := f.sendForPerson(t, "Dibran Mulder", testBirthDate)
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}
	if req := f.reconcile(t); req.ErrorCode != ErrorIdentityMismatch {
		t.Fatalf("code = %q, want %s", req.ErrorCode, ErrorIdentityMismatch)
	}
	f.requests.stored.Status, f.requests.stored.ErrorCode = StatusRejected, ErrorIdentityMismatch
	_, identity, err := f.svc.AdminRequestResult(context.Background(), testOrg.ID, sent.Request.ID)
	if err != nil {
		t.Fatalf("AdminRequestResult: %v", err)
	}
	if identity.Status != proofingprovider.StatusRejected || identity.ErrorCode != ErrorIdentityMismatch {
		t.Errorf("status = %s, code = %q; want rejected, %s", identity.Status, identity.ErrorCode, ErrorIdentityMismatch)
	}
	if identity.GivenName != "" || identity.FamilyName != "" || identity.BirthDate != "" {
		t.Errorf("identity = %+v, want no person", identity)
	}
}
