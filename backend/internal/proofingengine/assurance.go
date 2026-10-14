package proofingengine

import (
	"slices"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// assuranceInfo is what a session's evidence achieved, scored per sub-check
// (Categories), as opposed to the level a flow requires
// (flow.FlowDefinition.RequiredAssuranceLevel). Level and Score derive from
// the same item counts; flow.FlowDefinition.AssuranceTiers sets the cutoffs.
type assuranceInfo struct {
	// Level is the AssuranceTiers tier Score clears (flow.LevelForScore).
	Level string `json:"level"`
	// Score is ChecksPassed/ChecksTotal to 3 decimals; 0 with nothing to score.
	Score        float64                 `json:"score"`
	ChecksPassed int                     `json:"checksPassed"`
	ChecksTotal  int                     `json:"checksTotal"`
	Categories   []assuranceCategoryInfo `json:"categories,omitempty"`
	// EIDASLevel is the eIDAS level this session's checks proved
	// (computeEIDASAssuranceLevel), unlike Level (a percentage tier) and the
	// flow's required level.
	EIDASLevel flow.AssuranceLevel `json:"eidasLevel,omitempty"`
}

// assuranceCategoryInfo is one check's breakdown (checkItems).
type assuranceCategoryInfo struct {
	Check flow.Check `json:"check"`
	// State is the check's overall state. not_applicable (the capability did not
	// exist) and not_run (its step never ran) are left out of the counts.
	State       checkState `json:"state"`
	ItemsPassed int        `json:"itemsPassed"`
	ItemsTotal  int        `json:"itemsTotal"`
}

var defaultAssuranceChecks = []flow.Check{
	flow.CheckNFCPassiveAuth, flow.CheckNFCChipAuth, flow.CheckFaceMatch, flow.CheckFaceLiveness,
}

// assuranceChecksFor is the checks fd's steps are configured to perform: what
// the app is asked to run (appSessionView.RequiredChecks) and what the score
// counts. It decides no outcome and no eIDAS level.
func assuranceChecksFor(fd *flow.FlowDefinition) []flow.Check {
	wanted := defaultAssuranceChecks
	if fd != nil && len(fd.RequiredChecks) > 0 {
		wanted = fd.RequiredChecks
	}
	if fd != nil && slices.Contains(fd.Steps, flow.StepNFCRead) && !slices.Contains(wanted, flow.CheckNFCPassiveAuth) {
		wanted = append(slices.Clone(wanted), flow.CheckNFCPassiveAuth)
	}
	return wanted
}

// checkState is a check's (or sub-check's) outcome. Not a bool: a capability
// that did not apply (a chip without an AA key) is not a check that failed, and
// is left out of the score rather than counted against it.
type checkState string

const (
	// checkStatePass: the check ran and its criteria were met.
	checkStatePass checkState = "pass"
	// checkStateFail: the check ran, the capability existed, and its
	// criteria were not met.
	checkStateFail checkState = "fail"
	// checkStateNotApplicable: the capability does not exist for this
	// submission (no AA key on the chip). Excluded from scoring.
	checkStateNotApplicable checkState = "not_applicable"
	// checkStateNotRun: the step that produces it never ran. Excluded from
	// scoring.
	checkStateNotRun checkState = "not_run"
	// checkStateError: verification could not complete. Scored as a failure.
	checkStateError checkState = "error"
)

// boolCheckState maps a *bool fact (nil: never computed) to a checkState.
func boolCheckState(b *bool) checkState {
	if b == nil {
		return checkStateNotRun
	}
	if *b {
		return checkStatePass
	}
	return checkStateFail
}

type assuranceItem struct {
	State checkState
}

// checkItems splits one check into its sub-facts, each with its own state
// (nfc.passive_auth is the SOD signature, the hashes and the CSCA chain).
func checkItems(fd *flow.FlowDefinition, check flow.Check, req appResultRequest, checks *chipChecksInfo) []assuranceItem {
	switch check {
	case flow.CheckNFCPassiveAuth:
		var pa *passiveAuthInfo
		if checks != nil {
			pa = checks.PassiveAuthentication
		}
		if pa == nil {
			// No chip evidence: the nfc_read step never ran.
			return []assuranceItem{{State: checkStateNotRun}}
		}
		return []assuranceItem{
			{State: boolCheckState(pa.SODSignatureValid)},
			{State: boolCheckState(pa.DataGroupHashesValid)},
			{State: boolCheckState(pa.CSCATrustChainValid)},
		}
	case flow.CheckNFCChipAuth:
		var aa *activeAuthInfo
		if checks != nil {
			aa = checks.ActiveAuthentication
		}
		if aa == nil {
			// No chip read, or a chip without an AA/CA key: nothing to authenticate.
			return []assuranceItem{{State: checkStateNotApplicable}}
		}
		if aa.Attempted == nil || !*aa.Attempted {
			// The chip has a key but the app sent no challenge response.
			return []assuranceItem{{State: checkStateNotRun}}
		}
		return []assuranceItem{{State: boolCheckState(aa.Passed)}}
	case flow.CheckFaceMatch:
		if req.Biometrics == nil || req.Biometrics.FaceVerified == nil {
			return []assuranceItem{{State: checkStateNotRun}}
		}
		// A face that failed liveness is no live person matched, whatever the
		// score: Regula does not match one, and no other evidence may either.
		passed := *req.Biometrics.FaceVerified && checkOutcome(fd, flow.CheckFaceLiveness, req, checks) != checkStateFail
		if passed && fd != nil {
			if threshold, ok := fd.CheckThresholds[flow.CheckFaceMatch]; ok {
				passed = req.Biometrics.FaceMatchScore != nil && *req.Biometrics.FaceMatchScore >= threshold
			}
		}
		if passed {
			return []assuranceItem{{State: checkStatePass}}
		}
		return []assuranceItem{{State: checkStateFail}}
	case flow.CheckFaceLiveness:
		if req.Biometrics == nil {
			return []assuranceItem{{State: checkStateNotRun}}
		}
		// Regula's liveness verdict is authoritative without a score.
		if req.Biometrics.LivenessScore == nil && req.Biometrics.Engine != faceProviderRegula {
			// No liveness score (a provider without one, or the Yivi face check, which
			// runs no liveness): not applicable rather than a guessed verdict.
			return []assuranceItem{{State: checkStateNotApplicable}}
		}
		if req.Biometrics.LivenessResult == livenessPassed {
			return []assuranceItem{{State: checkStatePass}}
		}
		return []assuranceItem{{State: checkStateFail}}
	default:
		// A check nothing here computes (mrz.parse, viz.ocr, ...) scores as failed,
		// so listing one never overstates assurance.
		return []assuranceItem{{State: checkStateFail}}
	}
}

// overallCheckState is one state for a whole check: a failure wins over
// "did not apply", which wins over a pass, so a check passes only when every
// item did.
func overallCheckState(items []assuranceItem) checkState {
	sawFail, sawError, sawNotApplicable, sawNotRun, sawPass := false, false, false, false, false
	for _, item := range items {
		switch item.State {
		case checkStateFail:
			sawFail = true
		case checkStateError:
			sawError = true
		case checkStateNotApplicable:
			sawNotApplicable = true
		case checkStateNotRun:
			sawNotRun = true
		case checkStatePass:
			sawPass = true
		}
	}
	switch {
	case sawFail:
		return checkStateFail
	case sawError:
		return checkStateError
	case sawNotApplicable:
		return checkStateNotApplicable
	case sawNotRun:
		return checkStateNotRun
	case sawPass:
		return checkStatePass
	default:
		return checkStateNotRun
	}
}

func checkOutcome(fd *flow.FlowDefinition, check flow.Check, req appResultRequest, checks *chipChecksInfo) checkState {
	return overallCheckState(checkItems(fd, check, req, checks))
}

// faceMatchSource is what a session's face was matched against.
type faceMatchSource int

const (
	matchedChipPortrait faceMatchSource = iota
	matchedRelyingPartyPhoto
)

// faceMatchSourceOf is sess's: the customer's reference photo when it sent
// one, else the chip's own portrait.
func faceMatchSourceOf(sess session.Session) faceMatchSource {
	if sess.ReferencePhoto != "" {
		return matchedRelyingPartyPhoto
	}
	return matchedChipPortrait
}

// computeAssurance scores the evidence against fd's checks. Sub-checks that did
// not apply or did not run are left out of the score. source is what the face
// was matched against.
func computeAssurance(fd *flow.FlowDefinition, req appResultRequest, checks *chipChecksInfo, source faceMatchSource) assuranceInfo {
	wanted := assuranceChecksFor(fd)
	categories := make([]assuranceCategoryInfo, 0, len(wanted))
	passed, total := 0, 0
	for _, c := range wanted {
		items := checkItems(fd, c, req, checks)
		itemsPassed, itemsTotal := 0, 0
		for _, item := range items {
			switch item.State {
			case checkStatePass:
				itemsPassed++
				itemsTotal++
			case checkStateFail, checkStateError:
				itemsTotal++
			case checkStateNotApplicable, checkStateNotRun:
			}
		}
		categories = append(categories, assuranceCategoryInfo{
			Check: c, State: overallCheckState(items),
			ItemsPassed: itemsPassed, ItemsTotal: itemsTotal,
		})
		passed += itemsPassed
		total += itemsTotal
	}
	score := 0.0
	if total > 0 {
		score = round3(float64(passed) / float64(total))
	}
	tiers := flow.DefaultAssuranceTiers
	if fd != nil {
		tiers = fd.EffectiveAssuranceTiers()
	}
	return assuranceInfo{
		Level: flow.LevelForScore(tiers, score), Score: score,
		ChecksPassed: passed, ChecksTotal: total, Categories: categories,
		EIDASLevel: computeEIDASAssuranceLevel(fd, req, checks, source),
	}
}

// computeEIDASAssuranceLevel is the highest eIDAS level whose checks all
// verified, with the face verified by the level's provider against the chip
// portrait. Only Regula is certified, so another engine never lifts a face past
// low. Reported for every flow, independent of its required level; "" when not
// even low was reached. A check that did not apply or did not run (no AA key,
// AA not performed, an engine without a liveness result) does not count as
// verified.
func computeEIDASAssuranceLevel(fd *flow.FlowDefinition, req appResultRequest, checks *chipChecksInfo, source faceMatchSource) flow.AssuranceLevel {
	if fd == nil {
		return ""
	}
	achieved := flow.AssuranceLevel("")
	for _, level := range flow.LevelRequirements {
		if !meetsLevelRequirement(fd, level, req, checks, source) {
			break
		}
		achieved = level.Level
	}
	return achieved
}

// meetsLevelRequirement reports whether every check level needs passed on the
// evidence the session produced, whether or not fd lists it: an engine that
// reports no liveness leaves face.liveness not applicable, which never passes.
func meetsLevelRequirement(fd *flow.FlowDefinition, level flow.LevelRequirement, req appResultRequest, checks *chipChecksInfo, source faceMatchSource) bool {
	for _, c := range level.Checks {
		if checkOutcome(fd, c, req, checks) != checkStatePass {
			return false
		}
	}
	if level.FaceProvider == "" {
		return true
	}
	return source == matchedChipPortrait && req.Biometrics != nil && req.Biometrics.Engine == string(level.FaceProvider)
}
