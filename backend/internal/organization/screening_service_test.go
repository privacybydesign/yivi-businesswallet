package organization

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/vog"
)

// screeningStoreFake is the store as ScreeningService uses it: a minimal fake
// so the decision logic can be tested without a database, mirroring
// reverifyStub in reverify_service_test.go.
type screeningStoreFake struct {
	matchCtx  ScreeningMatchContext
	matchErr  error
	settings  ScreeningSettings
	recorded  []ScreeningInput
	recordErr error
}

func (f *screeningStoreFake) ScreeningMatchContext(context.Context, uuid.UUID, uuid.UUID) (ScreeningMatchContext, error) {
	return f.matchCtx, f.matchErr
}

func (f *screeningStoreFake) GetScreeningSettings(context.Context, uuid.UUID) (ScreeningSettings, error) {
	return f.settings, nil
}

func (f *screeningStoreFake) RecordScreening(_ context.Context, _, _ uuid.UUID, _ string, _ ScreeningSettings, in ScreeningInput, _ []byte) error {
	f.recorded = append(f.recorded, in)
	return f.recordErr
}

type validatorFake struct {
	code vog.ResponseCode
	err  error
}

func (v validatorFake) Validate(context.Context, []byte) (vog.ResponseCode, error) {
	return v.code, v.err
}

// parserFake stands in for the PDFium parser: the PDF bytes are irrelevant,
// the service is fed the Document (or error) the real parser would produce.
type parserFake struct {
	doc vog.Document
	err error
}

func (p parserFake) Parse([]byte) (vog.Document, error) {
	return p.doc, p.err
}

// matchingVOG is the document the real parser would read out of a VOG issued
// to fakeMatchContext's member (name and date of birth match).
func matchingVOG() parserFake {
	return parserFake{doc: vog.Document{
		GivenNames:  "Jan Willem",
		Surname:     "van der Jansen",
		DateOfBirth: "1990-04-03",
		IssueDate:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Reference:   "ABC12345XYZ",
		AspectCodes: []string{"11", "43"},
	}}
}

// somePDF is what a test hands UploadVog; parserFake never reads it.
var somePDF = []byte("%PDF-1.4 stand-in")

func fakeMatchContext() ScreeningMatchContext {
	dob := time.Date(1990, 4, 3, 0, 0, 0, 0, time.UTC)
	return ScreeningMatchContext{
		Name:        identity.Name{GivenNames: "Jan Willem", LastName: "van der Jansen"},
		DateOfBirth: &dob,
		MemberType:  MemberTypeEmployee,
		Email:       "jan@example.com",
	}
}

func TestUploadVogNoDateOfBirth(t *testing.T) {
	mc := fakeMatchContext()
	mc.DateOfBirth = nil
	store := &screeningStoreFake{matchCtx: mc}
	s := &ScreeningService{store: store, validator: validatorFake{code: vog.ResponseAuthentic}, parser: matchingVOG()}

	_, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, somePDF)
	if !errors.Is(err, ErrVogNoDateOfBirth) {
		t.Fatalf("err = %v, want ErrVogNoDateOfBirth", err)
	}
	if len(store.recorded) != 0 {
		t.Errorf("recorded %d screenings, want 0 (no check should have run)", len(store.recorded))
	}
}

func TestUploadVogGAAVRejection(t *testing.T) {
	store := &screeningStoreFake{matchCtx: fakeMatchContext()}
	s := &ScreeningService{store: store, validator: validatorFake{code: 2}, parser: matchingVOG()} // final rejection

	outcome, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, somePDF)
	if err != nil {
		t.Fatalf("UploadVog: %v", err)
	}
	if outcome.Result != vog.ResultRejected || outcome.RejectionReason != "gaav_rejected" {
		t.Errorf("outcome = %+v, want rejected/gaav_rejected", outcome)
	}
	if len(store.recorded) != 1 || store.recorded[0].Result != vog.ResultRejected {
		t.Errorf("recorded = %+v, want one rejected screening", store.recorded)
	}
}

func TestUploadVogNotAVOG(t *testing.T) {
	store := &screeningStoreFake{matchCtx: fakeMatchContext()}
	parser := parserFake{err: fmt.Errorf("%w: title not found", vog.ErrNotAVOG)}
	s := &ScreeningService{store: store, validator: validatorFake{code: vog.ResponseAuthentic}, parser: parser}

	outcome, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, somePDF)
	if err != nil {
		t.Fatalf("UploadVog: %v", err)
	}
	if outcome.Result != vog.ResultRejected || outcome.RejectionReason != "not_a_vog" {
		t.Errorf("outcome = %+v, want rejected/not_a_vog", outcome)
	}
	if len(store.recorded) != 1 {
		t.Errorf("recorded %d screenings, want 1 (a document verdict is kept)", len(store.recorded))
	}
}

func TestUploadVogUnparseable(t *testing.T) {
	store := &screeningStoreFake{matchCtx: fakeMatchContext()}
	parser := parserFake{err: fmt.Errorf("%w: geboortedatum: empty date", vog.ErrUnparseable)}
	s := &ScreeningService{store: store, validator: validatorFake{code: vog.ResponseAuthentic}, parser: parser}

	outcome, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, somePDF)
	if err != nil {
		t.Fatalf("UploadVog: %v", err)
	}
	if outcome.Result != vog.ResultRejected || outcome.RejectionReason != "unparseable" {
		t.Errorf("outcome = %+v, want rejected/unparseable", outcome)
	}
}

// TestUploadVogParserFailure: a parser infrastructure failure is not a verdict
// about the document and must not be recorded against the member.
func TestUploadVogParserFailure(t *testing.T) {
	store := &screeningStoreFake{matchCtx: fakeMatchContext()}
	parser := parserFake{err: errors.New("vog: get pdfium instance: timeout")}
	s := &ScreeningService{store: store, validator: validatorFake{code: vog.ResponseAuthentic}, parser: parser}

	_, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, somePDF)
	if err == nil {
		t.Fatal("UploadVog: want an error when the parser itself fails")
	}
	if len(store.recorded) != 0 {
		t.Errorf("recorded %d screenings, want 0", len(store.recorded))
	}
}

func TestUploadVogIdentityMismatch(t *testing.T) {
	mc := fakeMatchContext()
	mc.Name = identity.Name{GivenNames: "Someone", LastName: "Else"}
	store := &screeningStoreFake{matchCtx: mc}
	s := &ScreeningService{store: store, validator: validatorFake{code: vog.ResponseAuthentic}, parser: matchingVOG()}

	outcome, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, somePDF)
	if err != nil {
		t.Fatalf("UploadVog: %v", err)
	}
	if outcome.Result != vog.ResultMismatch || outcome.RejectionReason != "identity_mismatch" {
		t.Errorf("outcome = %+v, want mismatch/identity_mismatch", outcome)
	}
}

func TestUploadVogDateOfBirthMismatch(t *testing.T) {
	mc := fakeMatchContext()
	dob := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	mc.DateOfBirth = &dob
	store := &screeningStoreFake{matchCtx: mc}
	s := &ScreeningService{store: store, validator: validatorFake{code: vog.ResponseAuthentic}, parser: matchingVOG()}

	outcome, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, somePDF)
	if err != nil {
		t.Fatalf("UploadVog: %v", err)
	}
	if outcome.Result != vog.ResultMismatch {
		t.Errorf("outcome = %+v, want mismatch", outcome)
	}
}

func TestUploadVogInsufficientScope(t *testing.T) {
	store := &screeningStoreFake{
		matchCtx: fakeMatchContext(),
		settings: ScreeningSettings{RequiredFor: ScreeningRequiredForBoth, RequiredCodes: []string{"11", "91"}},
	}
	s := &ScreeningService{store: store, validator: validatorFake{code: vog.ResponseAuthentic}, parser: matchingVOG()}

	outcome, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, somePDF)
	if err != nil {
		t.Fatalf("UploadVog: %v", err)
	}
	if outcome.Result != vog.ResultInsufficientScope {
		t.Errorf("outcome = %+v, want insufficient_scope", outcome)
	}
	if want := []string{"91"}; !equalStrings(outcome.MissingCodes, want) {
		t.Errorf("MissingCodes = %v, want %v", outcome.MissingCodes, want)
	}
}

func TestUploadVogTooOld(t *testing.T) {
	maxAge := 30
	store := &screeningStoreFake{
		matchCtx: fakeMatchContext(),
		settings: ScreeningSettings{RequiredFor: ScreeningRequiredForBoth, MaxAgeAtUploadDays: &maxAge},
	}
	s := &ScreeningService{store: store, validator: validatorFake{code: vog.ResponseAuthentic}, parser: matchingVOG()}

	// matchingVOGPDF's issue date (01-02-2024) is far more than 30 days old.
	outcome, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, somePDF)
	if err != nil {
		t.Fatalf("UploadVog: %v", err)
	}
	if outcome.Result != vog.ResultRejected || outcome.RejectionReason != "too_old" {
		t.Errorf("outcome = %+v, want rejected/too_old", outcome)
	}
}

func TestUploadVogValid(t *testing.T) {
	store := &screeningStoreFake{
		matchCtx: fakeMatchContext(),
		settings: ScreeningSettings{RequiredFor: ScreeningRequiredForBoth, RequiredCodes: []string{"11"}},
	}
	s := &ScreeningService{store: store, validator: validatorFake{code: vog.ResponseAuthentic}, parser: matchingVOG()}

	outcome, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, somePDF)
	if err != nil {
		t.Fatalf("UploadVog: %v", err)
	}
	if outcome.Result != vog.ResultValid {
		t.Errorf("outcome = %+v, want valid", outcome)
	}
	if len(store.recorded) != 1 {
		t.Fatalf("recorded %d screenings, want 1", len(store.recorded))
	}
	rec := store.recorded[0]
	if want := []string{"11"}; !equalStrings(rec.CoveredCodes, want) {
		t.Errorf("CoveredCodes = %v, want %v", rec.CoveredCodes, want)
	}
	if rec.Reference != "ABC12345XYZ" {
		t.Errorf("Reference = %q, want ABC12345XYZ", rec.Reference)
	}
}

func TestUploadVogValidatorTransportError(t *testing.T) {
	store := &screeningStoreFake{matchCtx: fakeMatchContext()}
	s := &ScreeningService{store: store, validator: validatorFake{err: errors.New("boom")}, parser: matchingVOG()}

	_, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, somePDF)
	if err == nil {
		t.Fatal("UploadVog: want an error when the validator call itself fails")
	}
	if len(store.recorded) != 0 {
		t.Errorf("recorded %d screenings, want 0 (a transport failure records nothing)", len(store.recorded))
	}
}

// discloserFake is the wallet as the combined identity+VOG flow sees it.
type discloserFake struct {
	identity auth.DisclosedIdentity
	vog      auth.DisclosedVog
	err      error
}

func (d discloserFake) StartVogSession(context.Context, []string) (auth.Session, error) {
	return auth.Session{ID: "vog-session"}, nil
}

func (d discloserFake) DiscloseVog(context.Context, string, []string) (auth.DisclosedVog, error) {
	return d.vog, d.err
}

func (d discloserFake) StartIdentityVogSession(context.Context, []string) (auth.Session, error) {
	return auth.Session{ID: "identity-vog-session"}, nil
}

func (d discloserFake) DiscloseIdentityAndVog(context.Context, string, []string) (auth.DisclosedIdentity, auth.DisclosedVog, error) {
	return d.identity, d.vog, d.err
}

// identityRecorderFake stands in for Service.ApplyDisclosedIdentity: it
// records what it was asked to apply and answers with the name now on file.
type identityRecorderFake struct {
	applied *auth.DisclosedIdentity
	name    identity.Name
	err     error
}

func (r *identityRecorderFake) ApplyDisclosedIdentity(_ context.Context, _, _ uuid.UUID, _ string, _ identity.Name, disclosed auth.DisclosedIdentity) (identity.Name, error) {
	r.applied = &disclosed
	return r.name, r.err
}

func acceptingSettings(codes ...string) ScreeningSettings {
	return ScreeningSettings{RequiredFor: ScreeningRequiredForBoth, RequiredCodes: codes, AcceptYiviCredential: true}
}

// An unidentified member (no date of birth on file) discloses identity and VOG
// in one session: the identity is applied first and the VOG is matched against
// the date of birth it just disclosed.
func TestDiscloseIdentityAndVogCredentialValid(t *testing.T) {
	mc := fakeMatchContext()
	mc.DateOfBirth = nil
	mc.Name = identity.Name{} // never identified: no name on file either
	store := &screeningStoreFake{matchCtx: mc, settings: acceptingSettings("11")}
	recorder := &identityRecorderFake{name: identity.Name{GivenNames: "Jan Willem", LastName: "van der Jansen"}}
	discloser := discloserFake{
		identity: auth.DisclosedIdentity{
			Email:       "jan@example.com",
			Name:        identity.Name{GivenNames: "Jan Willem", LastName: "van der Jansen"},
			DateOfBirth: "1990-04-03",
		},
		vog: auth.DisclosedVog{
			GivenNames: "Jan Willem", Surname: "van der Jansen", DateOfBirth: "1990-04-03",
			IssueDate: time.Now().AddDate(0, -1, 0), AspectCodes: []string{"11"},
		},
	}
	s := &ScreeningService{store: store, discloser: discloser, identities: recorder}

	outcome, err := s.DiscloseIdentityAndVogCredential(context.Background(), uuid.New(), uuid.New(), CheckedBySelf, nil, "token")
	if err != nil {
		t.Fatalf("DiscloseIdentityAndVogCredential: %v", err)
	}
	if outcome.Result != vog.ResultValid {
		t.Errorf("outcome = %+v, want valid", outcome)
	}
	if recorder.applied == nil || recorder.applied.DateOfBirth != "1990-04-03" {
		t.Errorf("identity applied = %+v, want the disclosed identity", recorder.applied)
	}
	if len(store.recorded) != 1 || store.recorded[0].Method != vog.MethodYiviCredential {
		t.Errorf("recorded = %+v, want one credential screening", store.recorded)
	}
}

// The VOG is matched against the identity credential's date of birth, not its
// own: a VOG for someone else is a mismatch even though its two halves agree
// with each other.
func TestDiscloseIdentityAndVogCredentialMismatch(t *testing.T) {
	mc := fakeMatchContext()
	mc.DateOfBirth = nil
	store := &screeningStoreFake{matchCtx: mc, settings: acceptingSettings()}
	recorder := &identityRecorderFake{name: mc.Name}
	discloser := discloserFake{
		identity: auth.DisclosedIdentity{Email: "jan@example.com", Name: mc.Name, DateOfBirth: "1990-04-03"},
		vog:      auth.DisclosedVog{GivenNames: "Someone", Surname: "Else", DateOfBirth: "2000-01-01", IssueDate: time.Now()},
	}
	s := &ScreeningService{store: store, discloser: discloser, identities: recorder}

	outcome, err := s.DiscloseIdentityAndVogCredential(context.Background(), uuid.New(), uuid.New(), CheckedBySelf, nil, "token")
	if err != nil {
		t.Fatalf("DiscloseIdentityAndVogCredential: %v", err)
	}
	if outcome.Result != vog.ResultMismatch {
		t.Errorf("outcome = %+v, want mismatch", outcome)
	}
}

// A rejected identity half (e-mail or name mismatch, stale credential) is the
// re-identification error, and no screening is recorded - there is nothing
// trustworthy to match the VOG against.
func TestDiscloseIdentityAndVogCredentialIdentityRejected(t *testing.T) {
	store := &screeningStoreFake{matchCtx: fakeMatchContext(), settings: acceptingSettings()}
	recorder := &identityRecorderFake{err: ErrReverifyNameMismatch}
	s := &ScreeningService{store: store, discloser: discloserFake{}, identities: recorder}

	_, err := s.DiscloseIdentityAndVogCredential(context.Background(), uuid.New(), uuid.New(), CheckedBySelf, nil, "token")
	if !errors.Is(err, ErrReverifyNameMismatch) {
		t.Fatalf("err = %v, want ErrReverifyNameMismatch", err)
	}
	if len(store.recorded) != 0 {
		t.Errorf("recorded %d screenings, want 0", len(store.recorded))
	}
}

func TestDiscloseIdentityAndVogCredentialRequiresOptIn(t *testing.T) {
	store := &screeningStoreFake{matchCtx: fakeMatchContext(), settings: ScreeningSettings{RequiredFor: ScreeningRequiredForBoth}}
	s := &ScreeningService{store: store, discloser: discloserFake{}, identities: &identityRecorderFake{}}

	if _, err := s.StartIdentityVogCredentialSession(context.Background(), uuid.New()); !errors.Is(err, ErrVogCredentialNotAccepted) {
		t.Errorf("start: err = %v, want ErrVogCredentialNotAccepted", err)
	}
	if _, err := s.DiscloseIdentityAndVogCredential(context.Background(), uuid.New(), uuid.New(), CheckedBySelf, nil, "token"); !errors.Is(err, ErrVogCredentialNotAccepted) {
		t.Errorf("complete: err = %v, want ErrVogCredentialNotAccepted", err)
	}
}

func TestEvaluateCodes(t *testing.T) {
	covered, missing := evaluateCodes([]string{"11", "43", "91"}, []string{"11", "77"})
	if want := []string{"11"}; !equalStrings(covered, want) {
		t.Errorf("covered = %v, want %v", covered, want)
	}
	if want := []string{"77"}; !equalStrings(missing, want) {
		t.Errorf("missing = %v, want %v", missing, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
