package proofingengine

import (
	"context"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regula"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

func ptr[T any](v T) *T { return &v }

// verifiedChip is a chip read whose Passive and Active Authentication verified.
func verifiedChip() *chipChecksInfo {
	return &chipChecksInfo{
		PassiveAuthentication: &passiveAuthInfo{
			SODSignatureValid: ptr(true), DataGroupHashesValid: ptr(true), CSCATrustChainValid: ptr(true),
		},
		ActiveAuthentication: &activeAuthInfo{Attempted: ptr(true), Passed: ptr(true)},
	}
}

// chipWithoutAA is a verified chip that carries no Active Authentication key.
func chipWithoutAA() *chipChecksInfo {
	c := verifiedChip()
	c.ActiveAuthentication = nil
	return c
}

// chipAANotPerformed is a verified chip with a key the app sent no challenge
// response for.
func chipAANotPerformed() *chipChecksInfo {
	c := verifiedChip()
	c.ActiveAuthentication = &activeAuthInfo{Attempted: ptr(false), Passed: ptr(false)}
	return c
}

// chipAAFailed is a verified chip whose AA response did not verify.
func chipAAFailed() *chipChecksInfo {
	c := verifiedChip()
	c.ActiveAuthentication = &activeAuthInfo{Attempted: ptr(true), Passed: ptr(false)}
	return c
}

// selfieOnlyFlow reads the chip and captures a selfie it compares with
// nothing, listing neither face.liveness nor a required level.
func selfieOnlyFlow() *flow.FlowDefinition {
	return &flow.FlowDefinition{
		Steps:          []flow.Step{flow.StepNFCRead, flow.StepSelfie},
		RequiredChecks: []flow.Check{flow.CheckNFCPassiveAuth},
	}
}

// liveRegulaMatch is a live face Regula matched against the chip's portrait.
func liveRegulaMatch() appResultRequest {
	return appResultRequest{Biometrics: &biometricsInfo{
		FaceVerified: ptr(true), LivenessResult: "passed", Engine: faceProviderRegula,
	}}
}

// notLiveRegula is what Regula leaves after a failed liveness: no match ran.
func notLiveRegula() appResultRequest {
	return appResultRequest{Biometrics: &biometricsInfo{LivenessResult: "failed", Engine: faceProviderRegula}}
}

// chipFlow reads the chip and performs Passive Authentication only: enough
// for low.
func chipFlow(required flow.AssuranceLevel) *flow.FlowDefinition {
	return &flow.FlowDefinition{
		Steps:                  []flow.Step{flow.StepDocumentCapture, flow.StepNFCRead},
		RequiredChecks:         []flow.Check{flow.CheckNFCPassiveAuth},
		RequiredAssuranceLevel: required,
	}
}

// chipAndFaceFlow reads the chip and matches the face with Regula, performing
// the checks given: every substantial one makes it able to reach substantial.
func chipAndFaceFlow(required flow.AssuranceLevel, checks ...flow.Check) *flow.FlowDefinition {
	return &flow.FlowDefinition{
		Steps:          []flow.Step{flow.StepDocumentCapture, flow.StepNFCRead, flow.StepFaceVerification},
		SelfieLocation: flow.LocationNative, FaceProvider: flow.FaceProviderRegula,
		RequiredChecks: checks, RequiredAssuranceLevel: required,
	}
}

var substantialChecks = []flow.Check{flow.CheckNFCPassiveAuth, flow.CheckNFCChipAuth, flow.CheckFaceMatch, flow.CheckFaceLiveness}

func substantialFlow(required flow.AssuranceLevel) *flow.FlowDefinition {
	return chipAndFaceFlow(required, substantialChecks...)
}

// documentPhotoFlow only photographs the document: no step of it produces an
// assurance check.
func documentPhotoFlow(required flow.AssuranceLevel) *flow.FlowDefinition {
	return &flow.FlowDefinition{Steps: []flow.Step{flow.StepDocumentPhoto}, RequiredAssuranceLevel: required}
}

func TestComputeEIDASAssuranceLevel(t *testing.T) {
	untrusted := verifiedChip()
	untrusted.PassiveAuthentication.CSCATrustChainValid = ptr(false)
	notLiveButMatched := liveRegulaMatch()
	notLiveButMatched.Biometrics.LivenessResult = "failed"
	onDevice := liveRegulaMatch()
	onDevice.Biometrics.Engine, onDevice.Biometrics.LivenessScore = "on_device", ptr(0.9)
	noLivenessResult := liveRegulaMatch()
	noLivenessResult.Biometrics.Engine, noLivenessResult.Biometrics.LivenessResult = "iris", ""

	for _, tc := range []struct {
		name   string
		fd     *flow.FlowDefinition
		req    appResultRequest
		checks *chipChecksInfo
		source faceMatchSource
		want   flow.AssuranceLevel
	}{
		{"no applicable checks", documentPhotoFlow(""), appResultRequest{}, nil, matchedChipPortrait, ""},
		{"no flow claims none", nil, liveRegulaMatch(), verifiedChip(), matchedChipPortrait, ""},
		{"passive authentication passes", chipFlow(""), appResultRequest{}, verifiedChip(), matchedChipPortrait, flow.AssuranceLevelLow},
		{"untrusted CSCA", chipFlow(""), appResultRequest{}, untrusted, matchedChipPortrait, ""},
		{"all substantial checks pass", substantialFlow(flow.AssuranceLevelSubstantial), liveRegulaMatch(), verifiedChip(), matchedChipPortrait, flow.AssuranceLevelSubstantial},
		// The achieved level is computed without the required one: neither
		// capped by a lower requirement nor withheld without one.
		{"required low, substantial evidence", substantialFlow(flow.AssuranceLevelLow), liveRegulaMatch(), verifiedChip(), matchedChipPortrait, flow.AssuranceLevelSubstantial},
		{"no required level", substantialFlow(""), liveRegulaMatch(), verifiedChip(), matchedChipPortrait, flow.AssuranceLevelSubstantial},
		{"chip without an AA key reaches low", substantialFlow(flow.AssuranceLevelLow), liveRegulaMatch(), chipWithoutAA(), matchedChipPortrait, flow.AssuranceLevelLow},
		{"AA not performed", substantialFlow(""), liveRegulaMatch(), chipAANotPerformed(), matchedChipPortrait, flow.AssuranceLevelLow},
		{"liveness failed", substantialFlow(""), notLiveButMatched, verifiedChip(), matchedChipPortrait, flow.AssuranceLevelLow},
		{"face not by Regula", substantialFlow(""), onDevice, verifiedChip(), matchedChipPortrait, flow.AssuranceLevelLow},
		{"face not against the chip", substantialFlow(""), liveRegulaMatch(), verifiedChip(), matchedRelyingPartyPhoto, flow.AssuranceLevelLow},
		// The level follows the evidence produced, not the flow's checkbox: a
		// liveness result that passed counts even when face.liveness is not
		// listed, and an engine that reports no liveness leaves it not
		// applicable.
		{"liveness passed though not listed", chipAndFaceFlow("", flow.CheckNFCPassiveAuth, flow.CheckNFCChipAuth, flow.CheckFaceMatch), liveRegulaMatch(), verifiedChip(), matchedChipPortrait, flow.AssuranceLevelSubstantial},
		{"engine without a liveness result", substantialFlow(""), noLivenessResult, verifiedChip(), matchedChipPortrait, flow.AssuranceLevelLow},
		// The app runs AA only when nfc.chip_auth is listed: without it the
		// chip's key goes unused, and substantial is out of reach.
		{"AA not listed, so not performed", chipAndFaceFlow("", flow.CheckNFCPassiveAuth, flow.CheckFaceMatch, flow.CheckFaceLiveness), liveRegulaMatch(), chipAANotPerformed(), matchedChipPortrait, flow.AssuranceLevelLow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeEIDASAssuranceLevel(tc.fd, tc.req, tc.checks, tc.source); got != tc.want {
				t.Errorf("computeEIDASAssuranceLevel = %q, want %q", got, tc.want)
			}
		})
	}
}

// Active Authentication keeps apart a chip without a key, a key the app did
// not use, a signature that failed and one that verified.
func TestChipAuthStates(t *testing.T) {
	failed := verifiedChip()
	failed.ActiveAuthentication.Passed = ptr(false)
	for _, tc := range []struct {
		name   string
		checks *chipChecksInfo
		want   checkState
	}{
		{"no chip read", nil, checkStateNotApplicable},
		{"no key on the chip", chipWithoutAA(), checkStateNotApplicable},
		{"not performed", chipAANotPerformed(), checkStateNotRun},
		{"failed", failed, checkStateFail},
		{"passed", verifiedChip(), checkStatePass},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := checkOutcome(substantialFlow(""), flow.CheckNFCChipAuth, appResultRequest{}, tc.checks); got != tc.want {
				t.Errorf("nfc.chip_auth = %q, want %q", got, tc.want)
			}
		})
	}
}

// A face that failed liveness never counts as matched, whatever a score says.
func TestFaceMatchNeedsLiveness(t *testing.T) {
	req := liveRegulaMatch()
	req.Biometrics.LivenessResult = "failed"
	if got := checkOutcome(substantialFlow(""), flow.CheckFaceMatch, req, verifiedChip()); got == checkStatePass {
		t.Errorf("face.match = %q after a failed liveness", got)
	}
}

// livenessRegula answers every transaction of its tagged ones with status,
// and counts face matches.
type livenessRegula struct {
	*taggedRegula
	status  *int
	matches int
}

func (f *livenessRegula) GetLiveness(ctx context.Context, id string) (regula.LivenessTransaction, error) {
	tx, err := f.taggedRegula.GetLiveness(ctx, id)
	tx.Status = f.status
	return tx, err
}

func (f *livenessRegula) Match(context.Context, string, string) (regula.MatchResult, error) {
	f.matches++
	return regula.MatchResult{Similarity: 1}, nil
}

// Regula matches only a live capture: a failed liveness leaves no match.
func TestRegulaSkipsFailedLiveness(t *testing.T) {
	const notLive = 1
	client := &livenessRegula{taggedRegula: &taggedRegula{tags: map[string]string{"tx": "ips-ref"}}, status: ptr(notLive)}
	v := regulaFaceVerifier{client: client}
	ref := &faceReference{ImageBase64: "cG9ydHJhaXQ=", MimeType: "image/jpeg"}
	out, err := v.Verify(context.Background(), session.Session{TenantReference: "ref"}, ref, liveCapture{TransactionID: "tx"})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if out.LivenessPassed || out.Matched != nil || client.matches != 0 {
		t.Errorf("Verify = liveness %v, matched %v after %d matches; want no match", out.LivenessPassed, out.Matched, client.matches)
	}
}

func TestSessionOutcome(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	faceDone := session.Session{Steps: session.StepEvidence{Selfie: &session.SelfieStepEvidence{}}}
	noMatch := liveRegulaMatch()
	noMatch.Biometrics.FaceVerified = ptr(false)
	tampered := verifiedChip()
	tampered.TamperDetected = ptr(true)

	for _, tc := range []struct {
		name         string
		fd           *flow.FlowDefinition
		sess         session.Session
		req          appResultRequest
		checks       *chipChecksInfo
		wantAchieved flow.AssuranceLevel
		wantStatus   session.Status
		wantCode     string
	}{
		{"no checks, no requirement", documentPhotoFlow(""), session.Session{}, appResultRequest{}, nil, "", session.StatusApproved, ""},
		{"no checks, required low", documentPhotoFlow(flow.AssuranceLevelLow), session.Session{}, appResultRequest{}, nil, "", session.StatusRejected, errCodeAssuranceNotMet},
		{"required low, low achieved", chipFlow(flow.AssuranceLevelLow), session.Session{}, appResultRequest{}, verifiedChip(), flow.AssuranceLevelLow, session.StatusApproved, ""},
		{"required low, substantial achieved", substantialFlow(flow.AssuranceLevelLow), faceDone, liveRegulaMatch(), verifiedChip(), flow.AssuranceLevelSubstantial, session.StatusApproved, ""},
		{"no requirement, substantial achieved", substantialFlow(""), faceDone, liveRegulaMatch(), verifiedChip(), flow.AssuranceLevelSubstantial, session.StatusApproved, ""},
		{"required substantial, achieved", substantialFlow(flow.AssuranceLevelSubstantial), faceDone, liveRegulaMatch(), verifiedChip(), flow.AssuranceLevelSubstantial, session.StatusApproved, ""},
		{"required substantial, only low", substantialFlow(flow.AssuranceLevelSubstantial), faceDone, liveRegulaMatch(), chipWithoutAA(), flow.AssuranceLevelLow, session.StatusRejected, errCodeAssuranceNotMet},
		// nfc.chip_auth is listed and the chip has a key: a missing AA
		// response is a recorded read replayed without the chip, at any level.
		{"required substantial, AA not performed", substantialFlow(flow.AssuranceLevelSubstantial), faceDone, liveRegulaMatch(), chipAANotPerformed(), flow.AssuranceLevelLow, session.StatusRejected, errCodeChipCloneDetected},
		{"no requirement, AA not performed", substantialFlow(""), faceDone, liveRegulaMatch(), chipAANotPerformed(), flow.AssuranceLevelLow, session.StatusRejected, errCodeChipCloneDetected},
		{"no requirement, AA failed", substantialFlow(""), faceDone, liveRegulaMatch(), chipAAFailed(), flow.AssuranceLevelLow, session.StatusRejected, errCodeChipCloneDetected},
		// The app runs AA only when nfc.chip_auth is listed.
		{"AA not listed, not performed", chipAndFaceFlow("", flow.CheckNFCPassiveAuth, flow.CheckFaceMatch, flow.CheckFaceLiveness), faceDone, liveRegulaMatch(), chipAANotPerformed(), flow.AssuranceLevelLow, session.StatusApproved, ""},
		// AA is configured, but a chip without it still passes low.
		{"required low, chip without AA", substantialFlow(flow.AssuranceLevelLow), faceDone, liveRegulaMatch(), chipWithoutAA(), flow.AssuranceLevelLow, session.StatusApproved, ""},
		// A failed liveness gates only with a required level or face.liveness
		// listed. Otherwise the match decides, and Regula matched nothing.
		{"liveness failed, not gating", chipAndFaceFlow("", flow.CheckNFCPassiveAuth, flow.CheckFaceMatch), faceDone, notLiveRegula(), verifiedChip(), flow.AssuranceLevelLow, session.StatusRejected, errCodeFaceNoMatch},
		{"liveness failed, listed", chipAndFaceFlow("", flow.CheckNFCPassiveAuth, flow.CheckFaceMatch, flow.CheckFaceLiveness), faceDone, notLiveRegula(), verifiedChip(), flow.AssuranceLevelLow, session.StatusRejected, errCodeLivenessFailed},
		{"liveness failed, required low", chipAndFaceFlow(flow.AssuranceLevelLow, flow.CheckNFCPassiveAuth, flow.CheckFaceMatch), faceDone, notLiveRegula(), verifiedChip(), flow.AssuranceLevelLow, session.StatusRejected, errCodeLivenessFailed},
		{"liveness failed, capture only", selfieOnlyFlow(), faceDone, notLiveRegula(), verifiedChip(), flow.AssuranceLevelLow, session.StatusApproved, ""},
		{"face did not match", substantialFlow(""), faceDone, noMatch, verifiedChip(), flow.AssuranceLevelLow, session.StatusRejected, errCodeFaceNoMatch},
		{"tampered chip", chipFlow(""), session.Session{}, appResultRequest{}, tampered, flow.AssuranceLevelLow, session.StatusRejected, "DOC_TAMPERED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			achieved := computeEIDASAssuranceLevel(tc.fd, tc.req, tc.checks, matchedChipPortrait)
			if achieved != tc.wantAchieved {
				t.Errorf("achieved = %q, want %q", achieved, tc.wantAchieved)
			}
			status, code, _ := sessionOutcome(tc.fd, tc.sess, tc.req, tc.checks, achieved, now)
			if status != tc.wantStatus || code != tc.wantCode {
				t.Errorf("sessionOutcome = %s %q, want %s %q", status, code, tc.wantStatus, tc.wantCode)
			}
		})
	}
}
