package flow

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
)

func validFlow() FlowDefinition {
	return FlowDefinition{
		TenantID:       "tenant-a",
		Name:           "Standard NL onboarding",
		Steps:          []Step{StepDocumentCapture, StepNFCRead, StepSelfie, StepLiveness, StepFaceMatch},
		RequiredChecks: []Check{CheckNFCPassiveAuth, CheckFaceMatch},
	}
}

func TestValidateMinimalFlow(t *testing.T) {
	if err := Validate(validFlow()); err != nil {
		t.Fatalf("Validate(minimal valid flow) = %v, want nil", err)
	}
}

func TestValidateAgeOver(t *testing.T) {
	ok := validFlow()
	ok.RequestedAttributes, ok.AgeOver = []string{AttributeAgeOver}, 18
	if err := Validate(ok); err != nil {
		t.Fatalf("Validate(age_over 18) = %v, want nil", err)
	}
	withDocument := ok
	withDocument.RequestedAttributes = []string{AttributeAgeOver, "dg1"}
	if err := Validate(withDocument); err != nil {
		t.Fatalf("Validate(age_over with dg1) = %v, want nil", err)
	}

	noChip := ok
	noChip.Steps = []Step{StepDocumentPhoto}
	noChip.RequiredChecks = nil
	noAge, tooOld, ageOnly := ok, ok, validFlow()
	noAge.AgeOver = 0
	tooOld.AgeOver = MaxAgeOver + 1
	ageOnly.AgeOver = 18

	for name, tc := range map[string]struct {
		fd   FlowDefinition
		want string
	}{
		"no chip read":          {noChip, "requires nfc_read"},
		"no age":                {noAge, "ageOver must be between"},
		"too old":               {tooOld, "ageOver must be between"},
		"age without attribute": {ageOnly, "ageOver set without"},
	} {
		if err := Validate(tc.fd); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Validate(%s) = %v, want %q", name, err, tc.want)
		}
	}
}

func TestValidateRequiresName(t *testing.T) {
	fd := validFlow()
	fd.Name = "  "
	wantRefused(t, fd, "name is required")
}

func TestValidateNeedsAStep(t *testing.T) {
	fd := validFlow()
	fd.Steps = nil
	wantRefused(t, fd, "at least one step")
}

func TestValidateRejectsUnknownStep(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{"not_a_real_step"}
	wantRefused(t, fd, "unknown step")
}

func TestValidateDuplicateStep(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepSelfie, StepSelfie}
	wantRefused(t, fd, "duplicate step")
}

func TestValidateUnknownCheck(t *testing.T) {
	fd := validFlow()
	fd.RequiredChecks = []Check{"not_a_real_check"}
	wantRefused(t, fd, "unknown check")
}

func TestValidateDuplicateCheck(t *testing.T) {
	fd := validFlow()
	fd.RequiredChecks = []Check{CheckFaceMatch, CheckFaceMatch}
	wantRefused(t, fd, "duplicate check")
}

func TestValidateAcceptsKnownChecks(t *testing.T) {
	fd := validFlow()
	fd.RequiredChecks = []Check{CheckNFCPassiveAuth, CheckNFCChipAuth, CheckFaceMatch, CheckFaceLiveness}
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(known checks) = %v, want nil", err)
	}
}

func TestValidateUnknownLevel(t *testing.T) {
	fd := validFlow()
	fd.RequiredAssuranceLevel = "extreme"
	wantRefused(t, fd, "unknown assurance level")
}

func TestValidateAllowsEmptyLevel(t *testing.T) {
	fd := validFlow()
	fd.RequiredAssuranceLevel = ""
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(empty assurance level) = %v, want nil", err)
	}
}

func TestValidateRejectsHigh(t *testing.T) {
	fd := validFlow()
	fd.RequiredAssuranceLevel = AssuranceLevelHigh
	wantRefused(t, fd, "not achievable")
}

// substantialFlow requires everything LevelRequirements asks of substantial.
func substantialFlow() FlowDefinition {
	fd := validFlow()
	fd.Steps = []Step{StepDocumentCapture, StepNFCRead, StepFaceVerification}
	fd.RequiredChecks = []Check{CheckNFCPassiveAuth, CheckNFCChipAuth, CheckFaceMatch, CheckFaceLiveness}
	fd.SelfieLocation, fd.FaceProvider = LocationNative, FaceProviderRegula
	fd.RequiredAssuranceLevel = AssuranceLevelSubstantial
	return fd
}

func TestSubstantialAcceptsAll(t *testing.T) {
	if err := Validate(substantialFlow()); err != nil {
		t.Fatalf("Validate(substantial with every check and Regula) = %v, want nil", err)
	}
}

func TestSubstantialNeedsCheck(t *testing.T) {
	for _, missing := range []Check{CheckNFCChipAuth, CheckFaceLiveness} {
		fd := substantialFlow()
		fd.RequiredChecks = slices.DeleteFunc(slices.Clone(fd.RequiredChecks), func(c Check) bool { return c == missing })
		wantRefused(t, fd, "requiredAssuranceLevel \"substantial\" requires requiredChecks")
	}
}

func TestSubstantialNeedsRegula(t *testing.T) {
	fd := substantialFlow()
	fd.FaceProvider = FaceProviderEngine
	wantRefused(t, fd, "requires faceProvider")
}

func TestSubstantialNeedsEvidence(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepSelfie, StepFaceMatch}
	fd.RequiredChecks = []Check{CheckFaceMatch}
	fd.RequiredAssuranceLevel = AssuranceLevelSubstantial
	wantRefused(t, fd, "requiredAssuranceLevel \"substantial\" requires requiredChecks")
}

func TestSubstantialNeedsBinding(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepDocumentCapture, StepNFCRead}
	fd.RequiredChecks = []Check{CheckNFCPassiveAuth}
	fd.RequiredAssuranceLevel = AssuranceLevelSubstantial
	wantRefused(t, fd, "requiredAssuranceLevel \"substantial\" requires requiredChecks")
}

func TestValidateLowChipReadOnly(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepDocumentCapture, StepNFCRead}
	fd.RequiredChecks = []Check{CheckNFCPassiveAuth}
	fd.RequiredAssuranceLevel = AssuranceLevelLow
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(low with only the chip read) = %v, want nil", err)
	}
}

func TestValidateLowNoChipRead(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepSelfie, StepFaceMatch}
	fd.RequiredChecks = []Check{CheckFaceMatch}
	fd.RequiredAssuranceLevel = AssuranceLevelLow
	wantRefused(t, fd, "requiredAssuranceLevel \"low\" requires requiredChecks")
}

func TestValidateFaceNoMatchCheck(t *testing.T) {
	fd := validFlow()
	fd.RequiredChecks = []Check{CheckNFCPassiveAuth}
	wantRefused(t, fd, "face_verification requires requiredChecks to include face.match")
}

func TestValidateUnknownBSNPolicy(t *testing.T) {
	fd := validFlow()
	fd.BSNPolicy = "delete-immediately"
	wantRefused(t, fd, "unknown BSN policy")
}

func TestValidateKnownBSNPolicy(t *testing.T) {
	fd := validFlow()
	fd.BSNPolicy = privacy.BSNPolicyMask
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(mask BSN policy) = %v, want nil", err)
	}
}

func TestValidateUnknownLegalBasis(t *testing.T) {
	fd := validFlow()
	fd.LegalBasis = "marketing"
	wantRefused(t, fd, "unknown legal basis")
}

func TestValidateKnownLegalBasis(t *testing.T) {
	fd := validFlow()
	fd.LegalBasis = privacy.LegalBasisConsent
	fd.ProcessingPurpose = "AML/KYC customer onboarding"
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(consent legal basis) = %v, want nil", err)
	}
}

func TestValidateAllowsNoLegalBasis(t *testing.T) {
	fd := validFlow()
	fd.LegalBasis = ""
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(empty legal basis) = %v, want nil - not yet configured is not itself invalid", err)
	}
}

func TestValidateEmptyDocTypeEntry(t *testing.T) {
	fd := validFlow()
	fd.AcceptedDocumentTypes = []string{"P", ""}
	wantRefused(t, fd, "accepted document type entries must not be empty")
}

// A flow accepts a driving licence by its document type; the chip evidence's
// type is no document's, so accepting it could only fail.
func TestValidateLicence(t *testing.T) {
	fd := validFlow()
	fd.AcceptedDocumentTypes = []string{"P", DocumentTypeDrivingLicence}
	if err := Validate(fd); err != nil {
		t.Errorf("Validate(accepts %s) = %v", DocumentTypeDrivingLicence, err)
	}
	fd.AcceptedDocumentTypes = []string{"P", DocumentTypeEUDrivingLicence}
	wantRefused(t, fd, "never a document's")
}

// The flow editor uppercases every accepted type, a licence's included.
func TestAcceptsTypeIgnoresCase(t *testing.T) {
	fd := &FlowDefinition{AcceptedDocumentTypes: []string{"P", "DRIVERS_LICENSE"}}
	if !fd.AcceptsDocumentType(DocumentTypeDrivingLicence, DocumentTypeDrivingLicence) || !fd.AcceptsDocumentType("P", DocumentKindPassport) {
		t.Error("an accepted type was refused")
	}

	if fd.AcceptsDocumentType("I", DocumentKindIdentityCard) {
		t.Error("an unlisted type was accepted")
	}

	if !(&FlowDefinition{}).AcceptsDocumentType("I", DocumentKindIdentityCard) {
		t.Error("an unrestricted flow refused a type")
	}

	refused := validFlow()
	refused.AcceptedDocumentTypes = []string{"EU_DRIVING_LICENCE"}
	wantRefused(t, refused, "never a document's")
}

func TestValidateDuplicateCountry(t *testing.T) {
	fd := validFlow()
	fd.AcceptedIssuingCountries = []string{"NLD", "NLD"}
	wantRefused(t, fd, "duplicate accepted issuing country")
}

func TestValidateNFCNoCapture(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepNFCRead}
	wantRefused(t, fd, "must be included together")
}

func TestValidateNFCNoCheck(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepDocumentCapture, StepNFCRead}
	fd.RequiredChecks = nil
	wantRefused(t, fd, "nfc_read requires requiredChecks to include nfc.passive_auth")
}

// TestValidateNFCOnlyChipAuth checks that nfc.chip_auth alone
// no longer satisfies nfc_read's requirement - nfc.passive_auth is
// unconditionally mandatory now (nfc.chip_auth remains optional, since not
// every document carries an Active/Chip Authentication key).
func TestValidateNFCOnlyChipAuth(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepDocumentCapture, StepNFCRead}
	fd.RequiredChecks = []Check{CheckNFCChipAuth}
	wantRefused(t, fd, "nfc_read requires requiredChecks to include nfc.passive_auth")
}

// TestValidateNFCWithChipAuth checks that
// nfc.chip_auth may additionally be selected on top of the always-mandatory
// nfc.passive_auth - selecting it alone (without passive_auth) is rejected
// by TestValidateNFCOnlyChipAuth above.
func TestValidateNFCWithChipAuth(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepDocumentCapture, StepNFCRead}
	fd.RequiredChecks = []Check{CheckNFCPassiveAuth, CheckNFCChipAuth}
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(nfc_read with nfc.passive_auth + nfc.chip_auth required) = %v, want nil", err)
	}
}

func TestValidateNFCWithCapture(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepDocumentCapture, StepNFCRead}
	fd.RequiredChecks = []Check{CheckNFCPassiveAuth}
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(nfc_read with document_capture) = %v, want nil", err)
	}
}

func TestValidateFaceMatchNoNFC(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepSelfie, StepFaceMatch}
	fd.RequiredChecks = []Check{CheckFaceMatch}
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(face_match without nfc_read) = %v, want nil - a session created against this flow must supply its own reference photo, but that's a session.Session-level requirement, not a flow-level one", err)
	}
}

func TestValidateFaceMatchWithNFC(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepDocumentCapture, StepNFCRead, StepSelfie, StepLiveness, StepFaceMatch}
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(face_match with nfc_read) = %v, want nil", err)
	}
}

func TestValidateSelfieNoNFC(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepSelfie, StepLiveness}
	fd.RequiredChecks = nil
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(selfie/liveness without nfc_read, no face_match) = %v, want nil", err)
	}
}

// TestValidateCaptureNoNFC checks the reciprocal of
// TestValidateNFCNoCapture: document_capture is only
// ever fulfilled via the native nfc_read hand-off (there is no browser-side
// capture and no standalone native submission path either), so asking for
// it without nfc_read is unsatisfiable.
func TestValidateCaptureNoNFC(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepDocumentCapture}
	wantRefused(t, fd, "must be included together")
}

func TestValidateAllowsNoDocTypes(t *testing.T) {
	fd := validFlow()
	fd.AcceptedDocumentTypes = nil
	fd.AcceptedIssuingCountries = nil
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(no document type/country restriction) = %v, want nil (means \"any\")", err)
	}
}

func TestValidateThresholdRequired(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepSelfie, StepFaceMatch}
	fd.RequiredChecks = []Check{CheckFaceMatch}
	fd.CheckThresholds = map[Check]float64{CheckFaceMatch: 0.8}
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(threshold on required, thresholdable check) = %v, want nil", err)
	}
}

func TestThresholdNeedsItsCheck(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepSelfie, StepLiveness}
	fd.RequiredChecks = nil
	fd.CheckThresholds = map[Check]float64{CheckFaceMatch: 0.8}
	wantRefused(t, fd, "which is not in requiredChecks")
}

func TestValidateThresholdBadCheck(t *testing.T) {
	fd := validFlow()
	fd.CheckThresholds = map[Check]float64{CheckNFCPassiveAuth: 0.5}
	wantRefused(t, fd, "does not support a threshold")
}

func TestValidateThresholdRange(t *testing.T) {
	fd := validFlow()
	fd.CheckThresholds = map[Check]float64{CheckFaceMatch: 1.5}
	wantRefused(t, fd, "must be between 0 and 1")
}

func TestValidateNegativeRetention(t *testing.T) {
	fd := validFlow()
	fd.RetentionOverride = -time.Hour
	wantRefused(t, fd, "retentionOverride must be between")
}

func TestValidateRetentionOverYear(t *testing.T) {
	fd := validFlow()
	fd.RetentionOverride = MaxRetentionOverride + time.Second
	wantRefused(t, fd, "retentionOverride must be between")
}

func TestValidatePositiveRetention(t *testing.T) {
	fd := validFlow()
	fd.RetentionOverride = 24 * time.Hour
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(positive retentionOverride) = %v, want nil", err)
	}
}

func TestValidateUnknownSelfie(t *testing.T) {
	fd := validFlow()
	fd.SelfieLocation = "phone"
	wantRefused(t, fd, "unknown selfieLocation")
}

func TestValidateSelfieNoStep(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepDocumentCapture, StepNFCRead}
	fd.RequiredChecks = []Check{CheckNFCPassiveAuth}
	fd.SelfieLocation = LocationNative
	wantRefused(t, fd, "selfieLocation set without face_verification")
}

func TestValidateNativeSelfie(t *testing.T) {
	fd := validFlow()
	fd.SelfieLocation = LocationNative
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(native selfieLocation) = %v, want nil", err)
	}
}

func TestEffectiveSelfieDefault(t *testing.T) {
	fd := validFlow()
	if fd.EffectiveSelfieLocation() != LocationBrowser {
		t.Fatalf("EffectiveSelfieLocation() = %q, want browser", fd.EffectiveSelfieLocation())
	}
	fd.SelfieLocation = LocationNative
	if fd.EffectiveSelfieLocation() != LocationNative {
		t.Fatalf("explicit native override wasn't respected: %+v", fd)
	}
}

// ---- assurance tiers -----------------------------------------------------

func TestValidateAllowsEmptyTiers(t *testing.T) {
	if err := Validate(validFlow()); err != nil {
		t.Fatalf("Validate(no assuranceTiers) = %v, want nil", err)
	}
}

func TestValidateCustomLadder(t *testing.T) {
	fd := validFlow()
	fd.AssuranceTiers = []AssuranceTier{
		{Level: "gold", MinPercent: 0.9},
		{Level: "silver", MinPercent: 0.5},
		{Level: "bronze", MinPercent: 0},
	}
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(custom ladder) = %v, want nil", err)
	}
}

func TestValidateTierNoCatchAll(t *testing.T) {
	fd := validFlow()
	fd.AssuranceTiers = []AssuranceTier{{Level: "gold", MinPercent: 0.9}}
	wantRefused(t, fd, "must include a tier with minPercent 0")
}

func TestValidateTierEmptyLevel(t *testing.T) {
	fd := validFlow()
	fd.AssuranceTiers = []AssuranceTier{{Level: "  ", MinPercent: 0}}
	wantRefused(t, fd, "tier level must not be empty")
}

func TestValidateDuplicateTier(t *testing.T) {
	fd := validFlow()
	fd.AssuranceTiers = []AssuranceTier{
		{Level: "gold", MinPercent: 0.9},
		{Level: "gold", MinPercent: 0},
	}
	wantRefused(t, fd, "duplicate assurance tier level")
}

func TestValidateTierPercentRange(t *testing.T) {
	fd := validFlow()
	fd.AssuranceTiers = []AssuranceTier{{Level: "gold", MinPercent: 1.1}, {Level: "base", MinPercent: 0}}
	wantRefused(t, fd, "minPercent must be between 0 and 1")
	fd.AssuranceTiers = []AssuranceTier{{Level: "gold", MinPercent: -0.1}}
	wantRefused(t, fd, "minPercent must be between 0 and 1")
}

func TestEffectiveTiersDefault(t *testing.T) {
	fd := validFlow()
	got := fd.EffectiveAssuranceTiers()
	if len(got) != len(DefaultAssuranceTiers) {
		t.Fatalf("EffectiveAssuranceTiers() = %v, want DefaultAssuranceTiers", got)
	}
	custom := []AssuranceTier{{Level: "gold", MinPercent: 0.9}, {Level: "base", MinPercent: 0}}
	fd.AssuranceTiers = custom
	if got := fd.EffectiveAssuranceTiers(); len(got) != 2 {
		t.Fatalf("EffectiveAssuranceTiers() = %v, want the flow's own custom ladder", got)
	}
}

// TestLevelForScoreDefaultLadder pins
// DefaultAssuranceTiers against the exact worked example the ladder was
// derived from (see docs/session-model.md's "Assurance level" section):
// checksPassed/checksTotal of 0,1,2,3,4,5,6,7,8,9,10 out of 10.
func TestLevelForScoreDefaultLadder(t *testing.T) {
	cases := []struct {
		passed, total int
		want          string
	}{
		{0, 10, "super_low"},
		{1, 10, "super_low"},
		{2, 10, "super_low"},
		{3, 10, "low"},
		{4, 10, "low"},
		{5, 10, "medium"},
		{6, 10, "medium"},
		{7, 10, "high"},
		{8, 10, "very_high"},
		{9, 10, "very_high"},
		{10, 10, "perfect"},
	}
	for _, c := range cases {
		got := LevelForScore(DefaultAssuranceTiers, float64(c.passed)/float64(c.total))
		if got != c.want {
			t.Errorf("LevelForScore(%d/%d) = %q, want %q", c.passed, c.total, got, c.want)
		}
	}
}

func TestLevelForScoreCustomLadder(t *testing.T) {
	tiers := []AssuranceTier{
		{Level: "base", MinPercent: 0},
		{Level: "gold", MinPercent: 0.9}, // deliberately not sorted
		{Level: "silver", MinPercent: 0.5},
	}
	cases := []struct {
		score float64
		want  string
	}{{0, "base"}, {0.4, "base"}, {0.5, "silver"}, {0.89, "silver"}, {0.9, "gold"}, {1, "gold"}}
	for _, c := range cases {
		if got := LevelForScore(tiers, c.score); got != c.want {
			t.Errorf("LevelForScore(%.2f) = %q, want %q", c.score, got, c.want)
		}
	}
}

func TestLevelForScoreNoCatchAll(t *testing.T) {
	// Validate rejects this at save time (TestValidateTierNoCatchAll),
	// but LevelForScore itself must still degrade safely for a caller that
	// bypasses Validate (e.g. an older persisted flow version).
	tiers := []AssuranceTier{{Level: "gold", MinPercent: 0.9}}
	if got := LevelForScore(tiers, 0.1); got != "super_low" {
		t.Fatalf("LevelForScore with no catch-all = %q, want super_low", got)
	}
}

func TestValidateFaceProvider(t *testing.T) {
	face := func(p FaceProvider, loc StepLocation) FlowDefinition {
		return FlowDefinition{Name: "face", Steps: []Step{StepSelfie}, SelfieLocation: loc, FaceProvider: p}
	}
	valid := []FlowDefinition{
		face("", LocationNative),
		face(FaceProviderRegula, LocationNative),
		face(FaceProviderEngine, LocationBrowser),
	}
	for _, fd := range valid {
		if err := Validate(fd); err != nil {
			t.Errorf("Validate(%s, %s) = %v, want nil", fd.FaceProvider, fd.SelfieLocation, err)
		}
	}
	invalid := map[string]FlowDefinition{
		"unknown provider":     face("iris", LocationNative),
		"regula in browser":    face(FaceProviderRegula, LocationBrowser),
		"regula default place": face(FaceProviderRegula, ""),
		"no face step":         {Name: "nfc", Steps: []Step{StepNFCRead, StepDocumentCapture}, RequiredChecks: []Check{CheckNFCPassiveAuth}, FaceProvider: FaceProviderEngine},
	}
	for name, fd := range invalid {
		if err := Validate(fd); err == nil {
			t.Errorf("%s: Validate accepted it", name)
		}
	}
}

// wantRefused fails t unless Validate refuses fd by the rule whose message
// holds rule: another rule refusing it would let rule be deleted unnoticed.
func wantRefused(t *testing.T, fd FlowDefinition, rule string) {
	t.Helper()
	err := Validate(fd)
	var invalid *ValidationError
	if !errors.As(err, &invalid) || !strings.Contains(err.Error(), rule) {
		t.Errorf("Validate = %v, want a ValidationError by %q", err, rule)
	}
}

// A check needs a step that produces it: nfc.chip_auth without nfc_read is
// scored on nothing.
func TestValidateCheckWithoutStep(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepSelfie, StepFaceMatch}
	fd.RequiredChecks = []Check{CheckFaceMatch, CheckNFCChipAuth}
	wantRefused(t, fd, `check "nfc.chip_auth" requires one of steps`)
}

func TestValidIssuingCountry(t *testing.T) {
	for code, want := range map[string]bool{
		"NLD": true, "DEU": true, "BEL": true, "ATA": true, "EUE": true, "UNO": true, "RKS": true, "GBD": true,
		"NL": false, "nld": false, "D": false, "ZZZ": false, "XXX": false, "QQQ": false, "NLDD": false, "": false,
	} {
		if got := ValidIssuingCountry(code); got != want {
			t.Errorf("ValidIssuingCountry(%q) = %v, want %v", code, got, want)
		}
	}
}

// A flow naming a country code no document carries is refused, so it cannot
// be saved and then reject every document.
func TestValidateUnknownCountry(t *testing.T) {
	for _, code := range []string{"NL", "ZZZ", "nld"} {
		fd := validFlow()
		fd.AcceptedIssuingCountries = []string{"NLD", code}
		var verr *ValidationError
		if err := Validate(fd); !errors.As(err, &verr) {
			t.Errorf("Validate with %q = %v, want a ValidationError", code, err)
		}
	}
	fd := validFlow()
	fd.AcceptedIssuingCountries = []string{"NLD", "DEU"}
	if err := Validate(fd); err != nil {
		t.Errorf("Validate with NLD, DEU = %v, want nil", err)
	}
}

func TestIssuingStateCode(t *testing.T) {
	for in, want := range map[string]string{"D": "DEU", "D<<": "DEU", "NLD": "NLD", "DEU": "DEU"} {
		if got := IssuingStateCode(in); got != want {
			t.Errorf("IssuingStateCode(%q) = %q, want %q", in, got, want)
		}
	}
}

// An achieved level meets every requirement at or below it; nothing achieved
// meets none but the absent one, and a level the service cannot claim is
// never met.
func TestMeetsLevel(t *testing.T) {
	for _, tc := range []struct {
		achieved, required AssuranceLevel
		want               bool
	}{
		{"", "", true},
		{AssuranceLevelSubstantial, "", true},
		{"", AssuranceLevelLow, false},
		{AssuranceLevelLow, AssuranceLevelLow, true},
		{AssuranceLevelSubstantial, AssuranceLevelLow, true},
		{AssuranceLevelLow, AssuranceLevelSubstantial, false},
		{AssuranceLevelSubstantial, AssuranceLevelSubstantial, true},
		{AssuranceLevelSubstantial, AssuranceLevelHigh, false},
	} {
		if got := MeetsLevel(tc.achieved, tc.required); got != tc.want {
			t.Errorf("MeetsLevel(%q, %q) = %v, want %v", tc.achieved, tc.required, got, tc.want)
		}
	}
}

func TestDocumentKindOf(t *testing.T) {
	for _, c := range []struct{ code, issuer, holder, want string }{
		{"P<", "NLD", "NLD", DocumentKindPassport},
		{"PT", "NLD", "SYR", DocumentKindPassport},
		{"I<", "NLD", "NLD", DocumentKindIdentityCard},
		{"ID", "D<<", "D", DocumentKindIdentityCard},
		{"IR", "NLD", "TUR", DocumentKindResidencePermit},
		{"AR", "DEU", "", DocumentKindResidencePermit},
		// An ID code on a foreigner's card does not tell: a Belgian permit, an
		// Estonian identity card of an EU resident.
		{"ID", "BEL", "MAR", ""},
		{"ID", "EST", "FIN", ""},
		{"I<", "NLD", "", ""},
		{DocumentTypeDrivingLicence, "NLD", "", DocumentTypeDrivingLicence},
	} {
		if got := DocumentKindOf(c.code, c.issuer, c.holder); got != c.want {
			t.Errorf("DocumentKindOf(%q, %q, %q) = %q, want %q", c.code, c.issuer, c.holder, got, c.want)
		}
	}
}

func TestAcceptsTypeByCodeOrKind(t *testing.T) {
	accepts := func(accepted []string, code, kind string) bool {
		return (&FlowDefinition{AcceptedDocumentTypes: accepted}).AcceptsDocumentType(code, kind)
	}
	if !accepts([]string{"IR"}, "IR", DocumentKindResidencePermit) {
		t.Error("a listed code is accepted")
	}
	// The flow editor uppercases a kind too.
	if !accepts([]string{"RESIDENCE_PERMIT"}, "ID", DocumentKindResidencePermit) {
		t.Error("a listed kind is accepted whatever the code")
	}
	if accepts([]string{DocumentKindIdentityCard}, "ID", DocumentKindResidencePermit) {
		t.Error("a residence permit is not an identity card")
	}
	if accepts([]string{DocumentKindIdentityCard}, "I<", "") {
		t.Error("a document of unknown kind matches no kind")
	}
}
