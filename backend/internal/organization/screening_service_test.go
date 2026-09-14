package organization

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/vog"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/vog/vogtest"
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

func fakeMatchContext() ScreeningMatchContext {
	dob := time.Date(1990, 4, 3, 0, 0, 0, 0, time.UTC)
	return ScreeningMatchContext{
		Name:        identity.Name{GivenNames: "Jan Willem", LastName: "van der Jansen"},
		DateOfBirth: &dob,
		MemberType:  MemberTypeEmployee,
		Email:       "jan@example.com",
	}
}

func matchingVOGPDF(t *testing.T) []byte {
	return vogtest.BuildTestPDF(t, []string{
		"kenmerk: ABC12345XYZ",
		"Datum: 01-02-2024",
		"Geslachtsnaam: Jansen",
		"Tussenvoegsels: van der",
		"Voornamen: Jan Willem",
		"Geboortedatum: 03-04-1990",
		"profiel: 11 43",
	})
}

func TestUploadVogNoDateOfBirth(t *testing.T) {
	mc := fakeMatchContext()
	mc.DateOfBirth = nil
	store := &screeningStoreFake{matchCtx: mc}
	s := &ScreeningService{store: store, validator: validatorFake{code: vog.ResponseAuthentic}}

	_, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, matchingVOGPDF(t))
	if !errors.Is(err, ErrVogNoDateOfBirth) {
		t.Fatalf("err = %v, want ErrVogNoDateOfBirth", err)
	}
	if len(store.recorded) != 0 {
		t.Errorf("recorded %d screenings, want 0 (no check should have run)", len(store.recorded))
	}
}

func TestUploadVogGAAVRejection(t *testing.T) {
	store := &screeningStoreFake{matchCtx: fakeMatchContext()}
	s := &ScreeningService{store: store, validator: validatorFake{code: 2}} // final rejection

	outcome, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, matchingVOGPDF(t))
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
	s := &ScreeningService{store: store, validator: validatorFake{code: vog.ResponseAuthentic}}

	pdf := vogtest.BuildTestPDF(t, []string{"just some other document"})
	outcome, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, pdf)
	if err != nil {
		t.Fatalf("UploadVog: %v", err)
	}
	if outcome.Result != vog.ResultRejected || outcome.RejectionReason != "not_a_vog" {
		t.Errorf("outcome = %+v, want rejected/not_a_vog", outcome)
	}
}

func TestUploadVogIdentityMismatch(t *testing.T) {
	mc := fakeMatchContext()
	mc.Name = identity.Name{GivenNames: "Someone", LastName: "Else"}
	store := &screeningStoreFake{matchCtx: mc}
	s := &ScreeningService{store: store, validator: validatorFake{code: vog.ResponseAuthentic}}

	outcome, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, matchingVOGPDF(t))
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
	s := &ScreeningService{store: store, validator: validatorFake{code: vog.ResponseAuthentic}}

	outcome, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, matchingVOGPDF(t))
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
	s := &ScreeningService{store: store, validator: validatorFake{code: vog.ResponseAuthentic}}

	outcome, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, matchingVOGPDF(t))
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
	s := &ScreeningService{store: store, validator: validatorFake{code: vog.ResponseAuthentic}}

	// matchingVOGPDF's issue date (01-02-2024) is far more than 30 days old.
	outcome, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, matchingVOGPDF(t))
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
	s := &ScreeningService{store: store, validator: validatorFake{code: vog.ResponseAuthentic}}

	outcome, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, matchingVOGPDF(t))
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
	s := &ScreeningService{store: store, validator: validatorFake{err: errors.New("boom")}}

	_, err := s.UploadVog(context.Background(), uuid.New(), uuid.New(), "self", nil, matchingVOGPDF(t))
	if err == nil {
		t.Fatal("UploadVog: want an error when the validator call itself fails")
	}
	if len(store.recorded) != 0 {
		t.Errorf("recorded %d screenings, want 0 (a transport failure records nothing)", len(store.recorded))
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
