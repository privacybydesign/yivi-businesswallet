package flow

import (
	"slices"
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

func TestValidateAcceptsAMinimalFlow(t *testing.T) {
	if err := Validate(validFlow()); err != nil {
		t.Fatalf("Validate(minimal valid flow) = %v, want nil", err)
	}
}

func TestValidateRequiresName(t *testing.T) {
	fd := validFlow()
	fd.Name = "  "
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted a blank name")
	}
}

func TestValidateRequiresAtLeastOneStep(t *testing.T) {
	fd := validFlow()
	fd.Steps = nil
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted zero steps")
	}
}

func TestValidateRejectsUnknownStep(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{"not_a_real_step"}
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted an unknown step")
	}
}

func TestValidateRejectsDuplicateStep(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepSelfie, StepSelfie}
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted a duplicate step")
	}
}

func TestValidateRejectsUnknownCheck(t *testing.T) {
	fd := validFlow()
	fd.RequiredChecks = []Check{"not_a_real_check"}
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted an unknown check")
	}
}

func TestValidateRejectsDuplicateCheck(t *testing.T) {
	fd := validFlow()
	fd.RequiredChecks = []Check{CheckFaceMatch, CheckFaceMatch}
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted a duplicate check")
	}
}

func TestValidateAcceptsKnownChecks(t *testing.T) {
	fd := validFlow()
	fd.RequiredChecks = []Check{CheckNFCPassiveAuth, CheckNFCChipAuth, CheckFaceMatch, CheckFaceLiveness}
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(known checks) = %v, want nil", err)
	}
}

func TestValidateRejectsUnknownAssuranceLevel(t *testing.T) {
	fd := validFlow()
	fd.RequiredAssuranceLevel = "extreme"
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted an unknown assurance level")
	}
}

func TestValidateAcceptsEmptyAssuranceLevel(t *testing.T) {
	fd := validFlow()
	fd.RequiredAssuranceLevel = ""
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(empty assurance level) = %v, want nil", err)
	}
}

func TestValidateRejectsRequiredAssuranceLevelHigh(t *testing.T) {
	fd := validFlow()
	fd.RequiredAssuranceLevel = AssuranceLevelHigh
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted requiredAssuranceLevel \"high\" - no check this codebase computes reaches it (face.liveness is a heuristic, issue #7)")
	}
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

func TestValidateAcceptsRequiredAssuranceLevelSubstantialWithEveryCheck(t *testing.T) {
	if err := Validate(substantialFlow()); err != nil {
		t.Fatalf("Validate(substantial with every check and Regula) = %v, want nil", err)
	}
}

func TestValidateRejectsRequiredAssuranceLevelSubstantialMissingACheck(t *testing.T) {
	for _, missing := range []Check{CheckNFCChipAuth, CheckFaceLiveness} {
		fd := substantialFlow()
		fd.RequiredChecks = slices.DeleteFunc(slices.Clone(fd.RequiredChecks), func(c Check) bool { return c == missing })
		if err := Validate(fd); err == nil {
			t.Errorf("Validate accepted requiredAssuranceLevel \"substantial\" without %s", missing)
		}
	}
}

func TestValidateRejectsRequiredAssuranceLevelSubstantialWithoutRegula(t *testing.T) {
	fd := substantialFlow()
	fd.FaceProvider = FaceProviderEngine
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted requiredAssuranceLevel \"substantial\" on a face provider other than Regula")
	}
}

func TestValidateRejectsRequiredAssuranceLevelSubstantialWithoutEvidence(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepSelfie, StepFaceMatch}
	fd.RequiredChecks = []Check{CheckFaceMatch}
	fd.RequiredAssuranceLevel = AssuranceLevelSubstantial
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted requiredAssuranceLevel \"substantial\" with binding but no evidence-validation check required")
	}
}

func TestValidateRejectsRequiredAssuranceLevelSubstantialWithoutBinding(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepDocumentCapture, StepNFCRead}
	fd.RequiredChecks = []Check{CheckNFCPassiveAuth}
	fd.RequiredAssuranceLevel = AssuranceLevelSubstantial
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted requiredAssuranceLevel \"substantial\" with evidence but no face.match required")
	}
}

func TestValidateAcceptsRequiredAssuranceLevelLowWithChipReadOnly(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepDocumentCapture, StepNFCRead}
	fd.RequiredChecks = []Check{CheckNFCPassiveAuth}
	fd.RequiredAssuranceLevel = AssuranceLevelLow
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(low with only the chip read) = %v, want nil", err)
	}
}

func TestValidateRejectsRequiredAssuranceLevelLowWithoutChipRead(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepSelfie, StepFaceMatch}
	fd.RequiredChecks = []Check{CheckFaceMatch}
	fd.RequiredAssuranceLevel = AssuranceLevelLow
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted requiredAssuranceLevel \"low\" without the chip read's nfc.passive_auth")
	}
}

func TestValidateRejectsFaceVerificationWithoutFaceMatchRequiredCheck(t *testing.T) {
	fd := validFlow()
	fd.RequiredChecks = []Check{CheckNFCPassiveAuth}
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted face_verification steps with no face.match in requiredChecks")
	}
}

func TestValidateRejectsUnknownBSNPolicy(t *testing.T) {
	fd := validFlow()
	fd.BSNPolicy = "delete-immediately"
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted an unknown BSN policy")
	}
}

func TestValidateAcceptsKnownBSNPolicy(t *testing.T) {
	fd := validFlow()
	fd.BSNPolicy = privacy.BSNPolicyMask
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(mask BSN policy) = %v, want nil", err)
	}
}

func TestValidateRejectsUnknownLegalBasis(t *testing.T) {
	fd := validFlow()
	fd.LegalBasis = "marketing"
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted an unknown legal basis")
	}
}

func TestValidateAcceptsKnownLegalBasis(t *testing.T) {
	fd := validFlow()
	fd.LegalBasis = privacy.LegalBasisConsent
	fd.ProcessingPurpose = "AML/KYC customer onboarding"
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(consent legal basis) = %v, want nil", err)
	}
}

func TestValidateAcceptsEmptyLegalBasis(t *testing.T) {
	fd := validFlow()
	fd.LegalBasis = ""
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(empty legal basis) = %v, want nil - not yet configured is not itself invalid", err)
	}
}

func TestValidateRejectsEmptyDocumentTypeEntry(t *testing.T) {
	fd := validFlow()
	fd.AcceptedDocumentTypes = []string{"P", ""}
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted a blank accepted document type")
	}
}

func TestValidateRejectsDuplicateIssuingCountry(t *testing.T) {
	fd := validFlow()
	fd.AcceptedIssuingCountries = []string{"NLD", "NLD"}
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted a duplicate issuing country")
	}
}

func TestValidateRejectsNFCReadWithoutDocumentCapture(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepNFCRead}
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted an nfc_read step without document_capture")
	}
}

func TestValidateRejectsNFCReadWithoutMatchingRequiredCheck(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepDocumentCapture, StepNFCRead}
	fd.RequiredChecks = nil
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted an nfc_read step with no nfc.passive_auth in requiredChecks")
	}
}

// TestValidateRejectsNFCReadWithOnlyChipAuth checks that nfc.chip_auth alone
// no longer satisfies nfc_read's requirement - nfc.passive_auth is
// unconditionally mandatory now (nfc.chip_auth remains optional, since not
// every document carries an Active/Chip Authentication key).
func TestValidateRejectsNFCReadWithOnlyChipAuth(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepDocumentCapture, StepNFCRead}
	fd.RequiredChecks = []Check{CheckNFCChipAuth}
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted an nfc_read step with only nfc.chip_auth (no nfc.passive_auth) in requiredChecks")
	}
}

// TestValidateAcceptsNFCReadWithChipAuthRequiredCheck checks that
// nfc.chip_auth may additionally be selected on top of the always-mandatory
// nfc.passive_auth - selecting it alone (without passive_auth) is rejected
// by TestValidateRejectsNFCReadWithOnlyChipAuth above.
func TestValidateAcceptsNFCReadWithChipAuthRequiredCheck(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepDocumentCapture, StepNFCRead}
	fd.RequiredChecks = []Check{CheckNFCPassiveAuth, CheckNFCChipAuth}
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(nfc_read with nfc.passive_auth + nfc.chip_auth required) = %v, want nil", err)
	}
}

func TestValidateAcceptsNFCReadWithDocumentCapture(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepDocumentCapture, StepNFCRead}
	fd.RequiredChecks = []Check{CheckNFCPassiveAuth}
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(nfc_read with document_capture) = %v, want nil", err)
	}
}

func TestValidateAcceptsFaceMatchWithoutNFCRead(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepSelfie, StepFaceMatch}
	fd.RequiredChecks = []Check{CheckFaceMatch}
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(face_match without nfc_read) = %v, want nil - a session created against this flow must supply its own reference photo, but that's a session.Session-level requirement, not a flow-level one", err)
	}
}

func TestValidateAcceptsFaceMatchWithNFCRead(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepDocumentCapture, StepNFCRead, StepSelfie, StepLiveness, StepFaceMatch}
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(face_match with nfc_read) = %v, want nil", err)
	}
}

func TestValidateAcceptsSelfieAndLivenessWithoutNFCRead(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepSelfie, StepLiveness}
	fd.RequiredChecks = nil
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(selfie/liveness without nfc_read, no face_match) = %v, want nil", err)
	}
}

// TestValidateRejectsDocumentCaptureWithoutNFCRead checks the reciprocal of
// TestValidateRejectsNFCReadWithoutDocumentCapture: document_capture is only
// ever fulfilled via the native nfc_read hand-off (there is no browser-side
// capture and no standalone native submission path either), so asking for
// it without nfc_read is unsatisfiable.
func TestValidateRejectsDocumentCaptureWithoutNFCRead(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepDocumentCapture}
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted document_capture without nfc_read")
	}
}

func TestValidateAcceptsEmptyDocumentTypesAndCountries(t *testing.T) {
	fd := validFlow()
	fd.AcceptedDocumentTypes = nil
	fd.AcceptedIssuingCountries = nil
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(no document type/country restriction) = %v, want nil (means \"any\")", err)
	}
}

func TestValidateAcceptsThresholdForRequiredThresholdableCheck(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepSelfie, StepFaceMatch}
	fd.RequiredChecks = []Check{CheckFaceMatch}
	fd.CheckThresholds = map[Check]float64{CheckFaceMatch: 0.8}
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(threshold on required, thresholdable check) = %v, want nil", err)
	}
}

func TestValidateRejectsThresholdForCheckNotRequired(t *testing.T) {
	fd := validFlow()
	fd.RequiredChecks = nil
	fd.CheckThresholds = map[Check]float64{CheckFaceMatch: 0.8}
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted a threshold for a check not in RequiredChecks")
	}
}

func TestValidateRejectsThresholdForUnsupportedCheck(t *testing.T) {
	fd := validFlow()
	fd.RequiredChecks = []Check{CheckNFCPassiveAuth}
	fd.CheckThresholds = map[Check]float64{CheckNFCPassiveAuth: 0.5}
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted a threshold for a check that doesn't support one")
	}
}

func TestValidateRejectsOutOfRangeThreshold(t *testing.T) {
	fd := validFlow()
	fd.RequiredChecks = []Check{CheckFaceMatch}
	fd.CheckThresholds = map[Check]float64{CheckFaceMatch: 1.5}
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted a threshold above 1")
	}
}

func TestValidateRejectsNegativeRetentionOverride(t *testing.T) {
	fd := validFlow()
	fd.RetentionOverride = -time.Hour
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted a negative retentionOverride")
	}
}

func TestValidateAcceptsPositiveRetentionOverride(t *testing.T) {
	fd := validFlow()
	fd.RetentionOverride = 24 * time.Hour
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(positive retentionOverride) = %v, want nil", err)
	}
}

func TestValidateRejectsUnknownSelfieLocation(t *testing.T) {
	fd := validFlow()
	fd.SelfieLocation = "phone"
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted an unknown selfieLocation")
	}
}

func TestValidateRejectsSelfieLocationWithoutTheStep(t *testing.T) {
	fd := validFlow()
	fd.Steps = []Step{StepDocumentCapture, StepNFCRead}
	fd.RequiredChecks = []Check{CheckNFCPassiveAuth}
	fd.SelfieLocation = LocationNative
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted selfieLocation without selfie/liveness/face_match in steps")
	}
}

func TestValidateAcceptsNativeSelfieLocation(t *testing.T) {
	fd := validFlow()
	fd.SelfieLocation = LocationNative
	if err := Validate(fd); err != nil {
		t.Fatalf("Validate(native selfieLocation) = %v, want nil", err)
	}
}

func TestEffectiveSelfieLocationDefaultsToBrowser(t *testing.T) {
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

func TestValidateAcceptsEmptyAssuranceTiers(t *testing.T) {
	if err := Validate(validFlow()); err != nil {
		t.Fatalf("Validate(no assuranceTiers) = %v, want nil", err)
	}
}

func TestValidateAcceptsAWellFormedCustomLadder(t *testing.T) {
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

func TestValidateRejectsAssuranceTierMissingCatchAll(t *testing.T) {
	fd := validFlow()
	fd.AssuranceTiers = []AssuranceTier{{Level: "gold", MinPercent: 0.9}}
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted assuranceTiers with no minPercent-0 catch-all")
	}
}

func TestValidateRejectsAssuranceTierEmptyLevel(t *testing.T) {
	fd := validFlow()
	fd.AssuranceTiers = []AssuranceTier{{Level: "  ", MinPercent: 0}}
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted an assurance tier with a blank level")
	}
}

func TestValidateRejectsDuplicateAssuranceTierLevel(t *testing.T) {
	fd := validFlow()
	fd.AssuranceTiers = []AssuranceTier{
		{Level: "gold", MinPercent: 0.9},
		{Level: "gold", MinPercent: 0},
	}
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted a duplicate assurance tier level")
	}
}

func TestValidateRejectsOutOfRangeAssuranceTierMinPercent(t *testing.T) {
	fd := validFlow()
	fd.AssuranceTiers = []AssuranceTier{{Level: "gold", MinPercent: 1.1}, {Level: "base", MinPercent: 0}}
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted a minPercent above 1")
	}
	fd.AssuranceTiers = []AssuranceTier{{Level: "gold", MinPercent: -0.1}}
	if err := Validate(fd); err == nil {
		t.Fatal("Validate accepted a negative minPercent")
	}
}

func TestEffectiveAssuranceTiersDefaultsWhenUnset(t *testing.T) {
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

// TestLevelForScoreMatchesDefaultLadderAtEveryAnchor pins
// DefaultAssuranceTiers against the exact worked example the ladder was
// derived from (see docs/session-model.md's "Assurance level" section):
// checksPassed/checksTotal of 0,1,2,3,4,5,6,7,8,9,10 out of 10.
func TestLevelForScoreMatchesDefaultLadderAtEveryAnchor(t *testing.T) {
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

func TestLevelForScoreUsesACustomLadderInAnyOrder(t *testing.T) {
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

func TestLevelForScoreFallsBackToSuperLowWithoutACatchAll(t *testing.T) {
	// Validate rejects this at save time (TestValidateRejectsAssuranceTierMissingCatchAll),
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
