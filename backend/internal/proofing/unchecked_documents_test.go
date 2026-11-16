package proofing

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma"
)

// fakeUnchecked keeps unchecked files in memory, on the fixture's stored
// request as the store's columns read them.
type fakeUnchecked struct {
	requests *fakeRequests
	docs     []uncheckedDocument
}

func (f *fakeUnchecked) Add(_ context.Context, req Request, kind ReviewKind, holder diploma.Person, file DiplomaFile) (bool, error) {
	stored := f.requests.stored
	if kind == ReviewKindEmployment && (stored.SubjectEmployment != nil || stored.uncheckedStatements >= MaxUncheckedStatements) {
		return false, nil
	}
	f.docs = append(f.docs, uncheckedDocument{ID: uuid.New(), Kind: kind, FileName: file.Name, Holder: holder, Bytes: file.Bytes})
	stored.SealChecks = append(stored.SealChecks, SealCheck{Kind: kind, FileName: file.Name})
	if kind == ReviewKindEmployment {
		stored.UncheckedEmployment = true
		stored.uncheckedStatements++
	} else {
		stored.UncheckedDiplomas++
	}
	stored.setStepReview(kind, ReviewPending, nil)
	return true, nil
}

func (f *fakeUnchecked) List(_ context.Context, _ Request, kind ReviewKind) ([]uncheckedDocument, error) {
	var out []uncheckedDocument
	for _, d := range f.docs {
		if d.Kind == kind {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeUnchecked) Resolve(_ context.Context, _ Request, id uuid.UUID, outcome string) error {
	for i, d := range f.docs {
		if d.ID != id {
			continue
		}
		f.docs = append(f.docs[:i], f.docs[i+1:]...)
		for j, c := range f.requests.stored.SealChecks {
			if c.FileName == d.FileName && c.Outcome == "" {
				f.requests.stored.SealChecks[j].Outcome = outcome
				break
			}
		}
		if d.Kind == ReviewKindEmployment {
			f.requests.stored.uncheckedStatements--
			f.requests.stored.UncheckedEmployment = f.requests.stored.uncheckedStatements > 0
		} else {
			f.requests.stored.UncheckedDiplomas--
		}
		return nil
	}
	return nil
}

// withUnchecked keeps unchecked files in memory and retries without a wait.
func withUnchecked(d diplomaFixture) *fakeUnchecked {
	u := &fakeUnchecked{requests: d.requests}
	d.svc.unchecked, d.svc.sealRetryDelay = u, 0
	return u
}

func confirmStep(d diplomaFixture, req Request, kind ReviewKind) error {
	_, err := d.svc.DecideReviewItem(context.Background(), testOrg.ID, req.ID, kind, "rev@example.test",
		ReviewItemInput{Confirm: true, Reason: "checked", ReviewerID: uuid.New()})
	return err
}

func checkSealsAgain(d diplomaFixture, req Request, kind ReviewKind) (Request, error) {
	return d.svc.CheckSealsAgain(context.Background(), testOrg.ID, req.ID, kind, StepReviewInput{ReviewerID: uuid.New()})
}

// A seal the trust lists or a responder cannot vouch for is tried again, then
// kept for a reviewer: the files kept before it stay kept and are reported.
func TestUploadKeepsUnreachableSealForReview(t *testing.T) {
	d, _ := newDocumentFixture(DiplomasRequired, EmploymentRequired)
	unchecked := withUnchecked(d)
	d.checker.down = true
	req := d.sendApproved(t, time.Minute)

	verdicts, err := uploadDocuments(d, req, "UWV statement", "Anna Maria Jansen")
	if err != nil {
		t.Fatalf("AddDocuments: %v", err)
	}
	if len(verdicts) != 2 || verdicts[0].Employment == nil || verdicts[1].Reason != DocumentReasonSealPending {
		t.Fatalf("verdicts = %+v, want the statement kept and the diploma pending", verdicts)
	}
	if len(d.checker.holders) != sealCheckAttempts {
		t.Errorf("diploma checks = %d, want %d", len(d.checker.holders), sealCheckAttempts)
	}
	if len(unchecked.docs) != 1 || unchecked.docs[0].Kind != ReviewKindDiplomas || unchecked.docs[0].Holder.DateOfBirth == "" {
		t.Errorf("unchecked = %+v, want the diploma with its holder", unchecked.docs)
	}
	if d.requests.stored.DiplomasReview != ReviewPending {
		t.Errorf("diplomas review = %q, want pending", d.requests.stored.DiplomasReview)
	}
}

// After one file stayed unreachable through its tries, the next tries once.
func TestUnreachableSealTriesOnceAfterAnOutage(t *testing.T) {
	d := newDiplomaFixture(DiplomasRequired)
	withUnchecked(d)
	d.checker.down = true
	req := d.sendApproved(t, time.Minute)

	verdicts, err := d.upload(req, "Anna Maria Jansen", "Anna Jansen")
	if err != nil {
		t.Fatalf("AddDiplomas: %v", err)
	}
	for i, v := range verdicts {
		if v.Reason != DocumentReasonSealPending {
			t.Errorf("verdict %d = %+v, want pending", i, v)
		}
	}
	if len(d.checker.holders) != sealCheckAttempts+1 {
		t.Errorf("checks = %d, want %d", len(d.checker.holders), sealCheckAttempts+1)
	}
	if d.requests.stored.UncheckedDiplomas != 2 {
		t.Errorf("unchecked = %d, want 2", d.requests.stored.UncheckedDiplomas)
	}
}

// Statements whose seal waits count as given, but a fake one does not lock
// the real one out: up to MaxUncheckedStatements wait, and the check keeps
// the first that holds.
func TestUncheckedStatementsWaitTogether(t *testing.T) {
	d, e := newDocumentFixture(DiplomasOff, EmploymentRequired)
	withUnchecked(d)
	e.down = true
	req := d.sendApproved(t, time.Minute)
	for _, body := range []string{"signature_invalid", "real", "real again"} {
		v, err := d.svc.AddEmployment(context.Background(), testOrg.ID, req.ID, nil,
			DiplomaFile{Name: body + ".pdf", Bytes: []byte(body)})
		if err != nil || v.Reason != DocumentReasonSealPending {
			t.Fatalf("AddEmployment %s = %+v, %v; want it pending", body, v, err)
		}
	}
	stored := *d.requests.stored
	if err := d.svc.employmentOpen(stored); !errors.Is(err, ErrEmploymentClosed) {
		t.Errorf("employmentOpen with %d waiting = %v, want ErrEmploymentClosed", MaxUncheckedStatements, err)
	}
	if employmentUntil(stored) != nil || !newHostedProgress(stored, time.Now()).EmploymentGiven {
		t.Error("the subject's page does not read the statement as given")
	}

	e.down = false
	checked, err := checkSealsAgain(d, req, ReviewKindEmployment)
	if err != nil {
		t.Fatalf("CheckSealsAgain: %v", err)
	}
	var outcomes []string
	for _, c := range checked.SealChecks {
		outcomes = append(outcomes, c.Outcome)
	}
	if want := []string{"signature_invalid", SealCheckKept, DiplomaReasonDuplicate}; !slices.Equal(outcomes, want) {
		t.Errorf("outcomes = %v, want %v", outcomes, want)
	}
	if checked.SubjectEmployment == nil || checked.UncheckedEmployment {
		t.Errorf("kept %v, waiting %v; want the real one kept, none waiting", checked.SubjectEmployment, checked.UncheckedEmployment)
	}
}

// A reviewer confirms a step only once its seals were checked again.
func TestCheckSealsAgainBeforeConfirming(t *testing.T) {
	d := newDiplomaFixture(DiplomasRequired)
	unchecked := withUnchecked(d)
	d.checker.down = true
	req := d.sendApproved(t, time.Minute)
	if _, err := d.upload(req, "Anna Maria Jansen"); err != nil {
		t.Fatalf("AddDiplomas: %v", err)
	}

	if err := confirmStep(d, req, ReviewKindDiplomas); !errors.Is(err, ErrSealUnchecked) {
		t.Fatalf("confirm unchecked = %v, want ErrSealUnchecked", err)
	}
	if _, err := checkSealsAgain(d, req, ReviewKindDiplomas); !errors.Is(err, diploma.ErrRevocationUnavailable) {
		t.Fatalf("check again while down = %v, want ErrRevocationUnavailable", err)
	}
	if len(unchecked.docs) != 1 {
		t.Fatalf("unchecked after a failed check = %d, want it kept", len(unchecked.docs))
	}

	d.checker.down = false
	checked, err := checkSealsAgain(d, req, ReviewKindDiplomas)
	if err != nil {
		t.Fatalf("CheckSealsAgain: %v", err)
	}
	if checked.UncheckedDiplomas != 0 || len(unchecked.docs) != 0 || len(d.diplomas.held[req.ID]) != 1 {
		t.Errorf("after check: unchecked %d (%d kept), held %d; want the extract kept",
			checked.UncheckedDiplomas, len(unchecked.docs), len(d.diplomas.held[req.ID]))
	}
	if got := checked.SealChecks; len(got) != 1 || got[0].Outcome != SealCheckKept {
		t.Errorf("seal checks = %+v, want the extract kept", got)
	}
	if err := confirmStep(d, req, ReviewKindDiplomas); err != nil {
		t.Errorf("confirm after check: %v", err)
	}
}

// A statement checked again that does not name the person is refused.
func TestCheckSealsAgainRefusesAsAtUpload(t *testing.T) {
	d, e := newDocumentFixture(DiplomasOff, EmploymentRequired)
	withUnchecked(d)
	e.down = true
	req := d.sendApproved(t, time.Minute)
	if _, err := d.svc.AddEmployment(context.Background(), testOrg.ID, req.ID, nil,
		DiplomaFile{Name: "s.pdf", Bytes: []byte("holder_mismatch")}); err != nil {
		t.Fatalf("AddEmployment: %v", err)
	}

	e.down = false
	checked, err := checkSealsAgain(d, req, ReviewKindEmployment)
	if err != nil {
		t.Fatalf("CheckSealsAgain: %v", err)
	}
	if checked.UncheckedEmployment || checked.SubjectEmployment != nil {
		t.Errorf("after check: unchecked %v, kept %v; want it refused", checked.UncheckedEmployment, checked.SubjectEmployment)
	}
	if got := checked.SealChecks; len(got) != 1 || got[0].Outcome != "holder_mismatch" {
		t.Errorf("seal checks = %+v, want the statement refused holder_mismatch", got)
	}
	if len(d.requests.employmentRejections) != 1 {
		t.Errorf("rejections = %v, want one", d.requests.employmentRejections)
	}
}

func TestCheckSealsAgainNeedsAWaitingItem(t *testing.T) {
	d := newDiplomaFixture(DiplomasRequired)
	withUnchecked(d)
	req := d.sendApproved(t, time.Minute)
	if _, err := checkSealsAgain(d, req, ReviewKindDiplomas); !errors.Is(err, ErrNotUnderReview) {
		t.Errorf("check again without an item = %v, want ErrNotUnderReview", err)
	}
	if _, err := checkSealsAgain(d, req, ReviewKindAddress); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("check again on the address = %v, want ErrInvalidInput", err)
	}
}

// A fake and a real extract wait together: the check keeps the real one and
// refuses the fake, and the step can then be accepted with the real one only.
func TestCheckSealsAgainKeepsOnlyTheRealExtracts(t *testing.T) {
	d := newDiplomaFixture(DiplomasRequired)
	withUnchecked(d)
	d.checker.down = true
	req := d.sendApproved(t, time.Minute)
	if _, err := d.upload(req, "not a pdf", "Anna Maria Jansen"); err != nil {
		t.Fatalf("AddDiplomas: %v", err)
	}

	d.checker.down = false
	checked, err := checkSealsAgain(d, req, ReviewKindDiplomas)
	if err != nil {
		t.Fatalf("CheckSealsAgain: %v", err)
	}
	held := d.diplomas.held[req.ID]
	if len(held) != 1 || held[0].DocumentNumber != "Anna Maria Jansen" {
		t.Errorf("held = %+v, want the real extract only", held)
	}
	if got := checked.SealChecks; len(got) != 2 || got[0].Outcome != "not_a_diploma" || got[1].Outcome != SealCheckKept {
		t.Errorf("seal checks = %+v, want the fake refused and the real one kept", got)
	}
	if err := confirmStep(d, req, ReviewKindDiplomas); err != nil {
		t.Errorf("confirm with the real extract: %v", err)
	}
}

func TestSealRecheckFieldsMarkTheCheckAgain(t *testing.T) {
	plain := sealRecheckFields(context.Background(), map[string]any{"reason": "signature_invalid"})
	if _, ok := plain["sealCheckedAgain"]; ok {
		t.Errorf("an upload's fields = %v, want no mark", plain)
	}
	again := sealRecheckFields(withSealRecheck(context.Background(), "nep.pdf"), map[string]any{})
	if again["sealCheckedAgain"] != true || again["fileName"] != "nep.pdf" {
		t.Errorf("a check again's fields = %v, want it marked with the file", again)
	}
}
