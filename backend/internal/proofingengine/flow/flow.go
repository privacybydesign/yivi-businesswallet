// Package flow implements configurable identification flows
// (requirements.md §3: "Identification flow configurable per tenant,
// customer and process: steps, document types, countries, level of
// assurance") as versioned, validated, per-tenant objects: which steps a
// session walks through, which document types/issuing countries it
// accepts, which checks and level of assurance it requires, and the BSN/
// redaction policy that governs a Dutch document's handling for sessions
// created against it.
//
// A FlowDefinition is immutable once saved — editing one creates a new
// Version rather than mutating the old one, and a session records exactly
// the version it was created against (session.Session.FlowVersion), so
// editing a flow (or activating a different version) never changes the
// rules an in-flight or already-completed session is judged by. The session
// engine (internal/api's handleCreateSession/handleAppSessionResult)
// executes whichever FlowDefinition a session resolves to: which attributes
// get collected and which document types/checks are enforced come from
// Steps/AcceptedDocumentTypes/RequiredChecks — data — not a hard-coded
// per-method order in Go code.
package flow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
)

// ErrNotFound is returned when a flow id (or a specific version of one)
// doesn't exist for the given tenant.
var ErrNotFound = errors.New("flow: not found")

// Step is one stage of an identification flow's data collection — see
// FlowDefinition.Steps.
type Step string

const (
	// StepDocumentCapture is document identity capture — always fulfilled
	// via the native vcmrtd hand-off, alongside StepNFCRead (see Validate):
	// vcmrtd reads the document's own MRZ with its own camera (outside this
	// repo) to derive the chip access key, then submits the resulting
	// identity together with the chip evidence in one call
	// (POST .../steps/nfc — see api.documentEvidenceFromNativeDocument).
	// There is no browser-side document capture and no OCR/MRZ package
	// anywhere in this codebase; document identity capture happens only in
	// the app.
	StepDocumentCapture Step = "document_capture"
	// StepNFCRead is the ICAO 9303 chip read via the vcmrtd app (issue #1).
	StepNFCRead Step = "nfc_read"
	// StepDocumentPhoto is a photo of the document's printed data page or
	// card, taken in the vcmrtd app (POST .../steps/document_photo) and
	// returned as the result's documentImage. Always native; it stands on
	// its own, with or without the chip read.
	StepDocumentPhoto Step = "document_photo"
	// StepFaceVerification is the complete live face-verification stage.
	// Selfie capture, liveness, and face matching are capabilities of this
	// one stage, not separate stages.
	StepFaceVerification Step = "face_verification"
	// StepSelfie is the live selfie capture.
	StepSelfie Step = "selfie"
	// StepLiveness is the liveness check against the selfie.
	StepLiveness Step = "liveness"
	// StepFaceMatch is the selfie-vs-document/chip-photo match.
	StepFaceMatch Step = "face_match"
)

// validSteps is Step's closed vocabulary — see ValidStep.
var validSteps = map[Step]bool{
	StepDocumentCapture:  true,
	StepNFCRead:          true,
	StepDocumentPhoto:    true,
	StepFaceVerification: true,
	StepSelfie:           true,
	StepLiveness:         true,
	StepFaceMatch:        true,
}

// ValidStep reports whether s is a known Step.
func ValidStep(s Step) bool { return validSteps[s] }

// StepLocation names which client performs the selfie/liveness/face_match
// cluster: the browser (its own webcam + face capture, see the browser
// hosted flow, requirements.md §1) or the native app, vcmrtd. This is the
// only step cluster the choice applies to — StepDocumentCapture is always
// native (see its own doc comment) and StepNFCRead has no such choice at
// all, it's always native: no browser can do the ISO-DEP/APDU exchange
// ICAO 9303 documents need (Web NFC only exposes NDEF). See
// FlowDefinition.SelfieLocation.
type StepLocation string

const (
	// LocationBrowser is the default (empty also means this): the browser
	// hosted flow performs the step itself.
	LocationBrowser StepLocation = "browser"
	// LocationNative means vcmrtd performs the step instead — it still
	// posts evidence to the same step endpoints
	// (POST /api/v1/app/{token}/steps/...) the browser would otherwise
	// call; the endpoints don't care who submits evidence, only that it's
	// valid (see api/steps.go's package doc comment). This is purely a
	// client-orchestration signal: which UI screen the browser shows, and
	// which client is expected to call which endpoint.
	LocationNative StepLocation = "native"
)

// ValidStepLocation reports whether l is a known StepLocation, including
// the empty string (no override — see LocationBrowser).
func ValidStepLocation(l StepLocation) bool {
	switch l {
	case "", LocationBrowser, LocationNative:
		return true
	default:
		return false
	}
}

// EffectiveSelfieLocation resolves an empty override to its default
// (browser) — see FlowDefinition.SelfieLocation's doc comment.
func (fd FlowDefinition) EffectiveSelfieLocation() StepLocation {
	if fd.SelfieLocation == "" {
		return LocationBrowser
	}
	return fd.SelfieLocation
}

// FaceProvider names what verifies the face step's liveness and match: the
// Regula Face API or this server's own engine. Empty means the deployment
// default (api.Server.faceProviderFor).
type FaceProvider string

const (
	FaceProviderRegula FaceProvider = "regula"
	FaceProviderEngine FaceProvider = "engine"
)

// ValidFaceProvider reports whether p is a known FaceProvider, including "".
func ValidFaceProvider(p FaceProvider) bool {
	switch p {
	case "", FaceProviderRegula, FaceProviderEngine:
		return true
	default:
		return false
	}
}

// Check names one of the identity-proofing checks from requirements.md §4's
// vocabulary — see FlowDefinition.RequiredChecks.
type Check string

const (
	CheckMRZParse             Check = "mrz.parse"
	CheckMRZCheckdigits       Check = "mrz.checkdigits"
	CheckVIZOCR               Check = "viz.ocr"
	CheckVIZMRZCrossmatch     Check = "viz.mrz.crossmatch"
	CheckDocumentTemplate     Check = "document.template"
	CheckDocumentTamper       Check = "document.tamper"
	CheckDocumentScreenReplay Check = "document.screen_replay"
	CheckNFCPassiveAuth       Check = "nfc.passive_auth"
	CheckNFCChipAuth          Check = "nfc.chip_auth"
	CheckFaceMatch            Check = "face.match"
	CheckFaceLiveness         Check = "face.liveness"
	CheckChipVIZCrossmatch    Check = "chip.viz.crossmatch"
)

// validChecks is Check's closed vocabulary — see ValidCheck.
var validChecks = map[Check]bool{
	CheckMRZParse: true, CheckMRZCheckdigits: true, CheckVIZOCR: true, CheckVIZMRZCrossmatch: true,
	CheckDocumentTemplate: true, CheckDocumentTamper: true, CheckDocumentScreenReplay: true,
	CheckNFCPassiveAuth: true, CheckNFCChipAuth: true, CheckFaceMatch: true, CheckFaceLiveness: true,
	CheckChipVIZCrossmatch: true,
}

// ValidCheck reports whether c is a known Check.
func ValidCheck(c Check) bool { return validChecks[c] }

// thresholdableChecks is which Check values FlowDefinition.CheckThresholds
// may set a threshold for — see CheckThresholds' doc comment.
var thresholdableChecks = map[Check]bool{
	CheckFaceMatch: true,
}

// ThresholdSupported reports whether c is a Check that CheckThresholds may
// set a numeric threshold for.
func ThresholdSupported(c Check) bool { return thresholdableChecks[c] }

// checkStepBindings is which step(s) a step-scoped Check depends on — the
// step→check binding Validate enforces both directions of: a Step present
// without its mandatory Check is rejected above (nfc_read needs
// nfc.passive_auth, face_verification/face_match needs face.match), and
// selecting a Check here without any of its steps is rejected below (e.g.
// nfc.chip_auth selected on a flow with no nfc_read step). Checks absent
// from this list (the vocabulary-only ones — mrz.parse, viz.ocr, ...) have
// no step this codebase actually computes them from yet, so nothing to bind.
var checkStepBindings = []struct {
	check      Check
	anyOfSteps []Step
}{
	{CheckNFCPassiveAuth, []Step{StepNFCRead}},
	{CheckNFCChipAuth, []Step{StepNFCRead}},
	{CheckFaceMatch, []Step{StepFaceVerification, StepFaceMatch}},
	// face.liveness is computed off of any selfie submission
	// (api.checkLiveness runs unconditionally in handleSubmitSelfieStep),
	// so it's available whenever any step in the selfie/liveness/face_match
	// cluster is present, not just the narrower face.match pair above.
	{CheckFaceLiveness, []Step{StepFaceVerification, StepSelfie, StepLiveness, StepFaceMatch}},
}

// AssuranceLevel is an eIDAS level of assurance (requirements.md §2).
type AssuranceLevel string

const (
	AssuranceLevelLow         AssuranceLevel = "low"
	AssuranceLevelSubstantial AssuranceLevel = "substantial"
	AssuranceLevelHigh        AssuranceLevel = "high"
)

// ValidAssuranceLevel reports whether l is a known AssuranceLevel, including
// the empty string (no assurance-level requirement configured).
func ValidAssuranceLevel(l AssuranceLevel) bool {
	switch l {
	case "", AssuranceLevelLow, AssuranceLevelSubstantial, AssuranceLevelHigh:
		return true
	default:
		return false
	}
}

// LevelRequirement is what a flow must require, and a session must pass, to
// reach an eIDAS level (Implementing Regulation (EU) 2015/1502 §2.1.2): every
// check in Checks verified, and the face verified by FaceProvider when set.
// A check's steps follow from checkStepBindings.
type LevelRequirement struct {
	Level        AssuranceLevel
	Checks       []Check
	FaceProvider FaceProvider
}

// LevelRequirements is every level this service can claim, lowest first.
// High is absent: no check computed here is certified anti-spoofing.
//
//   - low: the subject holds genuine evidence — a chip read whose Passive
//     Authentication verifies (SOD signature, data-group hashes, CSCA chain).
//   - substantial: low, the chip proven original (Active Authentication), and
//     the subject bound to it — a live (liveness) face matched by Regula
//     against the chip's own portrait.
var LevelRequirements = []LevelRequirement{
	{Level: AssuranceLevelLow, Checks: []Check{CheckNFCPassiveAuth}},
	{
		Level:        AssuranceLevelSubstantial,
		Checks:       []Check{CheckNFCPassiveAuth, CheckNFCChipAuth, CheckFaceMatch, CheckFaceLiveness},
		FaceProvider: FaceProviderRegula,
	},
}

// RequirementFor returns l's LevelRequirement; ok is false for a level this
// service cannot claim.
func RequirementFor(l AssuranceLevel) (LevelRequirement, bool) {
	for _, r := range LevelRequirements {
		if r.Level == l {
			return r, true
		}
	}
	return LevelRequirement{}, false
}

// FlowDefinition is one version of one tenant's configurable identification
// flow — see the package doc comment.
type FlowDefinition struct {
	// ID identifies the flow across all its versions; stable once assigned
	// by Store.Save (a caller creating a brand-new flow leaves this empty).
	ID       string
	TenantID string
	// Version starts at 1 and increments by one with every Store.Save call
	// against the same ID — never reused, never reordered.
	Version int
	Name    string
	// Steps is the ordered sequence of Step the session engine walks a
	// session through — this is the "no hard-coded step order" data: the
	// app-facing session view (api.appSessionView.Steps) echoes this order
	// directly, and RequestedAttributes is derived from it (see
	// api.attributesForSteps).
	Steps []Step
	// RequestedAttributes controls which result data the tenant receives;
	// it is independent from Steps and may omit any optional attribute.
	RequestedAttributes           []string
	RequestedAttributesConfigured bool
	// SelfieLocation configures which client performs the selfie/liveness/
	// face_match cluster — the browser hosted flow (default, empty means
	// LocationBrowser) or vcmrtd natively (LocationNative). Only
	// meaningful when one of those steps is actually in Steps — Validate
	// rejects setting it otherwise. document_capture has no equivalent
	// field: it's always fulfilled via the native nfc_read hand-off (see
	// StepDocumentCapture's doc comment), and nfc_read itself has no
	// choice either — it's always native, a hard platform constraint, not
	// a configuration choice. See StepLocation's doc comment for what
	// "native" actually changes (client orchestration only, not which
	// endpoint accepts the evidence).
	SelfieLocation StepLocation
	// FaceProvider picks the face verifier for this flow's face step; empty
	// inherits the deployment default. Regula needs selfieLocation native,
	// since only vcmrtd runs a Regula liveness session today.
	FaceProvider FaceProvider
	// AcceptedDocumentTypes restricts which documentInfo.Type values a
	// session's submitted result may report (empty means any).
	AcceptedDocumentTypes []string
	// AcceptedIssuingCountries restricts which documentInfo.IssuingState
	// values (ICAO 3-letter codes) are accepted (empty means any).
	AcceptedIssuingCountries []string
	// RequiredChecks lists which of Check's vocabulary this flow scores into
	// its sessions' assurance info (api.computeAssurance) — despite the
	// name, not a gate on session success: whether a session reaches
	// StatusApproved depends only on whether its Steps completed
	// (api.requiredStepsComplete) and on hard security failures (a
	// tampered/cloned chip — api.authenticityFailure), never on whether an
	// individual check here passed. What a check here does affect is how
	// high the session's resulting assurance score/eIDAS level can land.
	//
	// Each step-scoped check here requires the step(s) that produce it to
	// be in Steps — Validate rejects a mismatched selection (checkStepBindings).
	// Some checks are structurally mandatory whenever their step is present
	// (CheckNFCPassiveAuth for StepNFCRead, CheckFaceMatch for
	// StepFaceVerification/StepFaceMatch) and Validate rejects omitting
	// them; others (CheckNFCChipAuth, CheckFaceLiveness) are optional —
	// included only when a flow author explicitly selects them, and simply
	// score lower/excluded (never reject a session) when the underlying
	// capability doesn't apply to a given submission (e.g. no Active
	// Authentication key on the chip, no anti-spoof model wired into the
	// serving engine — see api.checkItems' NOT_APPLICABLE state). Only
	// checks this codebase can actually compute today (CheckNFCPassiveAuth,
	// CheckNFCChipAuth, CheckFaceMatch, CheckFaceLiveness) contribute
	// anything to scoring; listing any other check documents intent without
	// anything computing it yet.
	RequiredChecks []Check
	// CheckThresholds optionally sets a minimum numeric score for one of
	// RequiredChecks — today only meaningful for CheckFaceMatch, the only
	// required check this codebase computes a comparable score for (see
	// api.biometricsInfo.FaceMatchScore); every other check is a pass/fail
	// gate with nothing to threshold. A check absent from this map is
	// enforced pass/fail only, exactly as before. Validate rejects a
	// threshold for a check that isn't in RequiredChecks, or for one that
	// doesn't support a threshold at all.
	CheckThresholds        map[Check]float64
	RequiredAssuranceLevel AssuranceLevel
	// BSNPolicy/BlurFace/BlurBSN, when this flow definition governs a
	// session, override the tenant-level defaults (tenant.Tenant.BSNPolicy/
	// Redaction) — requirements.md §3's "stored per tenant with overrides
	// per sub-tenant and per process" ("process" being what a flow
	// definition models here) and "option to blur the photo and/or the
	// BSN" (independent knobs). Each overrides independently, and only if
	// the flow actually sets it: an empty BSNPolicy or a nil BlurFace/
	// BlurBSN means this flow doesn't have its own opinion on that one
	// setting, so the tenant's default is used instead (see api.Server's
	// merge of a resolved flow with tenantPrivacyPolicy) — otherwise
	// setting only blurBsn on a flow would silently force blurFace to
	// false too, rather than leaving it to the tenant's default.
	BSNPolicy privacy.BSNPolicy
	BlurFace  *bool
	BlurBSN   *bool
	// RetentionOverride, when non-zero, overrides api.Config.SessionRetention
	// for sessions this flow governs (requirements.md §3: "retention
	// override") — a session stamps the effective duration at creation (see
	// session.Session.RetentionOverride), so editing or reactivating a
	// different flow version afterward never changes how long an
	// already-created session is retained. Zero means no override: the
	// deployment's global default applies, same as before this field
	// existed.
	RetentionOverride time.Duration
	// AssuranceTiers optionally overrides DefaultAssuranceTiers — the
	// checksPassed/checksTotal percentage ladder api.computeAssurance maps a
	// session's *achieved* assurance onto (not to be confused with
	// RequiredAssuranceLevel above, an eIDAS low/substantial/high bar set in
	// advance; this is what the session's own evidence actually achieved,
	// scored after the fact). Different tenants/flows can have different
	// risk tolerances — what counts as "high" for a low-stakes signup needn't
	// match what counts as "high" for a financial account opening — so this
	// is configured per flow, the same override pattern as BSNPolicy/
	// BlurFace/CheckThresholds above. Nil/empty means DefaultAssuranceTiers
	// applies unchanged. See EffectiveAssuranceTiers/LevelForScore.
	AssuranceTiers []AssuranceTier
	// LegalBasis/ProcessingPurpose, when set, override the tenant-level
	// defaults (tenant.Tenant.LegalBasis/ProcessingPurpose) for sessions this
	// flow governs — the same independent, empty-means-inherit override
	// convention as BSNPolicy/BlurFace/BlurBSN above: a flow with different
	// GDPR grounds than its tenant's default (e.g. one flow runs under
	// consent, another under a legal obligation) can say so without changing
	// the tenant's own setting. api.Server.processingBasis resolves the two
	// together for the evidence report.
	LegalBasis        privacy.LegalBasis
	ProcessingPurpose string
	// Active marks this as the version Store.Get(..., version: 0) and
	// Store.List return for its ID — exactly one version per (TenantID, ID)
	// is active at a time.
	Active    bool
	CreatedAt time.Time
}

// AssuranceTier is one rung of a checksPassed/checksTotal percentage-to-level
// ladder — see FlowDefinition.AssuranceTiers.
type AssuranceTier struct {
	// Level is a free-form label (e.g. "super_low", "high", "gold") — unlike
	// AssuranceLevel above, this isn't a closed eIDAS vocabulary: a flow
	// picking its own tier ladder is also free to name its own rungs.
	Level string `json:"level"`
	// MinPercent is the minimum checksPassed/checksTotal ratio (0..1,
	// inclusive) this tier requires — LevelForScore returns whichever tier
	// has the highest MinPercent the session's score still clears.
	MinPercent float64 `json:"minPercent"`
}

// DefaultAssuranceTiers is the ladder a session's achieved assurance is
// scored against when its flow doesn't configure its own (see
// FlowDefinition.AssuranceTiers) — every session, everywhere, starts here
// unless a tenant/flow deliberately opts into a different one.
var DefaultAssuranceTiers = []AssuranceTier{
	{Level: "perfect", MinPercent: 1},
	{Level: "very_high", MinPercent: 0.8},
	{Level: "high", MinPercent: 0.7},
	{Level: "medium", MinPercent: 0.5},
	{Level: "low", MinPercent: 0.3},
	{Level: "super_low", MinPercent: 0},
}

// EffectiveAssuranceTiers resolves fd.AssuranceTiers to DefaultAssuranceTiers
// when the flow hasn't set its own — the same "empty means default" pattern
// EffectiveSelfieLocation uses.
func (fd FlowDefinition) EffectiveAssuranceTiers() []AssuranceTier {
	if len(fd.AssuranceTiers) == 0 {
		return DefaultAssuranceTiers
	}
	return fd.AssuranceTiers
}

// LevelForScore returns the Level of whichever tier in tiers has the
// highest MinPercent that score (0..1) still clears (score >= MinPercent).
// tiers need not be sorted. Falls back to "super_low" if nothing in tiers
// clears score at all — Validate normally guarantees every AssuranceTiers
// list includes a MinPercent-0 catch-all, but a caller bypassing Validate
// (e.g. an older persisted flow version saved before this field existed)
// shouldn't get an empty/undefined level just because tiers is empty.
func LevelForScore(tiers []AssuranceTier, score float64) string {
	best, bestPercent := "super_low", -1.0
	for _, t := range tiers {
		if score >= t.MinPercent && t.MinPercent > bestPercent {
			best, bestPercent = t.Level, t.MinPercent
		}
	}
	return best
}

// Validate checks fd for structural correctness. Store.Save calls this
// before persisting anything, so an invalid flow definition is rejected
// outright rather than silently stored and only failing later, mid-session.
func Validate(fd FlowDefinition) error {
	if strings.TrimSpace(fd.Name) == "" {
		return errors.New("flow: name is required")
	}
	if len(fd.Steps) == 0 {
		return errors.New("flow: at least one step is required")
	}
	seenSteps := map[Step]bool{}
	for _, st := range fd.Steps {
		if !ValidStep(st) {
			return fmt.Errorf("flow: unknown step %q", st)
		}
		if seenSteps[st] {
			return fmt.Errorf("flow: duplicate step %q", st)
		}
		seenSteps[st] = true
	}
	// document_capture and nfc_read are always submitted together: vcmrtd
	// is document_capture's only source (it reads the document's own MRZ
	// with its own camera, outside this repo, to derive the BAC/PACE chip
	// access key), delivered alongside mrtdEvidence in one
	// POST .../steps/nfc call (see api.documentEvidenceFromNativeDocument)
	// — there is no standalone "document_capture but no chip" submission
	// path, and no reason to ask for nfc_read without also wanting the
	// document identity it carries.
	if seenSteps[StepNFCRead] != seenSteps[StepDocumentCapture] {
		return errors.New("flow: document_capture and nfc_read must be included together (document_capture is only ever fulfilled via the native nfc_read hand-off)")
	}
	if !ValidStepLocation(fd.SelfieLocation) {
		return fmt.Errorf("flow: unknown selfieLocation %q", fd.SelfieLocation)
	}
	hasFaceVerification := seenSteps[StepFaceVerification] || seenSteps[StepSelfie] || seenSteps[StepLiveness] || seenSteps[StepFaceMatch]
	if fd.SelfieLocation != "" && !hasFaceVerification {
		return errors.New("flow: selfieLocation set without face_verification in steps")
	}
	if !ValidFaceProvider(fd.FaceProvider) {
		return fmt.Errorf("flow: unknown faceProvider %q", fd.FaceProvider)
	}
	if fd.FaceProvider != "" && !hasFaceVerification {
		return errors.New("flow: faceProvider set without face_verification in steps")
	}
	if fd.FaceProvider == FaceProviderRegula && fd.EffectiveSelfieLocation() != LocationNative {
		return errors.New("flow: faceProvider regula requires selfieLocation native")
	}
	// Note: StepFaceMatch without StepNFCRead is a valid combination -
	// each step is meant to work standalone, so a flow can ask for face
	// verification without also reading the chip. StepNFCRead's DG2 photo
	// is only one of two possible comparison-photo sources; the other is a
	// reference photo the relying party supplies when creating a session
	// (session.Session.ReferencePhotoImage). That requirement is enforced
	// at session-creation time (api.handleCreateSession), not here: whether
	// a photo was actually supplied is a per-session concern, not something
	// a flow definition (governing many sessions, forever) can check.
	if err := noDuplicateStrings("accepted document type", fd.AcceptedDocumentTypes); err != nil {
		return err
	}
	if err := noDuplicateStrings("accepted issuing country", fd.AcceptedIssuingCountries); err != nil {
		return err
	}
	seenChecks := map[Check]bool{}
	for _, c := range fd.RequiredChecks {
		if !ValidCheck(c) {
			return fmt.Errorf("flow: unknown check %q", c)
		}
		if seenChecks[c] {
			return fmt.Errorf("flow: duplicate check %q", c)
		}
		seenChecks[c] = true
	}
	// A step alone doesn't mean much is actually scored - it only controls
	// what gets collected/requested (api.attributesForSteps) and what an app
	// UI shows, not which checks feed a session's assurance score/eIDAS
	// level (that's RequiredChecks, scored in api.computeAssurance).
	// nfc.passive_auth is structurally mandatory whenever nfc_read is a
	// step - it's the baseline trust anchor for any chip read (SOD
	// signature, per-data-group hashes, CSCA trust chain) and every chip
	// read can compute it, unlike nfc.chip_auth which depends on the
	// document actually carrying an Active/Chip Authentication key. Without
	// this rule, a flow whose Steps list nfc_read but whose RequiredChecks
	// omits it would never score the chip's own authenticity at all - the
	// flow would look thorough but the resulting assurance score wouldn't
	// reflect that.
	if seenSteps[StepNFCRead] && !seenChecks[CheckNFCPassiveAuth] {
		return errors.New("flow: nfc_read requires requiredChecks to include nfc.passive_auth (mandatory for every nfc_read step - nfc.chip_auth remains optional)")
	}
	// Same rule, for binding rather than evidence: a flow whose Steps list
	// face_verification/face_match but whose RequiredChecks omits face.match
	// would never score whether the applicant is who the evidence describes
	// at all. This is also what makes the eIDAS binding requirement
	// (docs/compliance.md §2) structural rather than a judgement call left
	// to whoever configures the flow. Deliberately narrower than
	// hasFaceVerification above: StepSelfie/StepLiveness alone (capture, or
	// capture+liveness, with no comparison) is a legitimate standalone
	// configuration - api.handleSubmitSelfieStep's own needsFaceMatch uses
	// this same narrower pair, only StepFaceVerification/StepFaceMatch ever
	// trigger an actual comparison.
	needsFaceMatch := seenSteps[StepFaceVerification] || seenSteps[StepFaceMatch]
	if needsFaceMatch && !seenChecks[CheckFaceMatch] {
		return errors.New("flow: face_verification requires requiredChecks to include face.match (mandatory whenever face_verification/face_match is a step)")
	}
	// The reverse pairing: selecting a step-scoped check without the step
	// that actually produces it would select a check nothing ever computes
	// for this flow (checkItems always resolves it to NOT_RUN/NOT_APPLICABLE)
	// - almost certainly a configuration mistake, e.g. selecting
	// nfc.chip_auth on a flow with no nfc_read step at all.
	for _, binding := range checkStepBindings {
		if !seenChecks[binding.check] {
			continue
		}
		hasStep := false
		for _, st := range binding.anyOfSteps {
			if seenSteps[st] {
				hasStep = true
				break
			}
		}
		if !hasStep {
			return fmt.Errorf("flow: check %q requires one of steps %v in Steps", binding.check, binding.anyOfSteps)
		}
	}
	for check, threshold := range fd.CheckThresholds {
		if !seenChecks[check] {
			return fmt.Errorf("flow: threshold set for check %q which is not in requiredChecks", check)
		}
		if !ThresholdSupported(check) {
			return fmt.Errorf("flow: check %q does not support a threshold", check)
		}
		if threshold < 0 || threshold > 1 {
			return fmt.Errorf("flow: threshold for check %q must be between 0 and 1", check)
		}
	}
	if !ValidAssuranceLevel(fd.RequiredAssuranceLevel) {
		return fmt.Errorf("flow: unknown assurance level %q", fd.RequiredAssuranceLevel)
	}
	// A required level is reachable only when the flow requires every check
	// its LevelRequirement does (and, through checkStepBindings above, their
	// steps): otherwise every session would fall short of it.
	if err := validateRequiredLevel(fd, seenChecks); err != nil {
		return err
	}
	if !privacy.ValidBSNPolicy(fd.BSNPolicy) {
		return fmt.Errorf("flow: unknown BSN policy %q", fd.BSNPolicy)
	}
	if !privacy.ValidLegalBasis(fd.LegalBasis) {
		return fmt.Errorf("flow: unknown legal basis %q", fd.LegalBasis)
	}
	if fd.RetentionOverride < 0 {
		return errors.New("flow: retentionOverride must not be negative")
	}
	if err := validateAssuranceTiers(fd.AssuranceTiers); err != nil {
		return err
	}
	return nil
}

// validateRequiredLevel rejects a RequiredAssuranceLevel fd's checks and face
// provider cannot reach (see LevelRequirements).
func validateRequiredLevel(fd FlowDefinition, seenChecks map[Check]bool) error {
	if fd.RequiredAssuranceLevel == "" {
		return nil
	}
	req, ok := RequirementFor(fd.RequiredAssuranceLevel)
	if !ok {
		return fmt.Errorf("flow: requiredAssuranceLevel %q is not achievable by any check this service computes", fd.RequiredAssuranceLevel)
	}
	for _, c := range req.Checks {
		if !seenChecks[c] {
			return fmt.Errorf("flow: requiredAssuranceLevel %q requires requiredChecks to include %s", req.Level, joinChecks(req.Checks))
		}
	}
	if req.FaceProvider != "" && fd.FaceProvider != req.FaceProvider {
		return fmt.Errorf("flow: requiredAssuranceLevel %q requires faceProvider %q", req.Level, req.FaceProvider)
	}
	return nil
}

func joinChecks(checks []Check) string {
	names := make([]string, len(checks))
	for i, c := range checks {
		names[i] = string(c)
	}
	return strings.Join(names, ", ")
}

// validateAssuranceTiers rejects a malformed AssuranceTiers list outright,
// the same "invalid means never persisted" guarantee Validate gives every
// other field — a tier with no catch-all (nothing at MinPercent 0) would
// leave LevelForScore silently falling back to "super_low" for a score
// range the flow author never actually considered, rather than the error
// this should be at save time.
func validateAssuranceTiers(tiers []AssuranceTier) error {
	seenLevels := map[string]bool{}
	hasZero := false
	for _, t := range tiers {
		if strings.TrimSpace(t.Level) == "" {
			return errors.New("flow: assurance tier level must not be empty")
		}
		if seenLevels[t.Level] {
			return fmt.Errorf("flow: duplicate assurance tier level %q", t.Level)
		}
		seenLevels[t.Level] = true
		if t.MinPercent < 0 || t.MinPercent > 1 {
			return fmt.Errorf("flow: assurance tier %q minPercent must be between 0 and 1", t.Level)
		}
		if t.MinPercent == 0 {
			hasZero = true
		}
	}
	if len(tiers) > 0 && !hasZero {
		return errors.New("flow: assuranceTiers must include a tier with minPercent 0 (a catch-all for the lowest scores)")
	}
	return nil
}

func noDuplicateStrings(label string, values []string) error {
	seen := map[string]bool{}
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("flow: %s entries must not be empty", label)
		}
		if seen[v] {
			return fmt.Errorf("flow: duplicate %s %q", label, v)
		}
		seen[v] = true
	}
	return nil
}

// Store is a persisted, versioned, per-tenant flow-definition registry
// (see PostgresStore). Only created once a database is configured, the same
// convention as tenant.Store — an operator-facing admin tool has no
// equivalent here because, unlike tenants/keys, flow definitions are meant
// to be self-service (see tenant.ScopeFlowsManage): api.Config.Flows wires
// this into the HTTP API directly.
type Store interface {
	// Save validates fd and persists it as a new version, marking it
	// active. An empty fd.ID creates a brand-new flow: an ID is generated
	// and Version is set to 1. A non-empty fd.ID must already exist for
	// fd.TenantID (ErrNotFound otherwise); Version is set to one more than
	// the highest version already stored for it. Returns fd with
	// ID/Version/Active/CreatedAt filled in.
	Save(ctx context.Context, fd FlowDefinition) (FlowDefinition, error)

	// Get returns one version of tenantID's flowID. version == 0 means
	// "whichever version is currently active".
	Get(ctx context.Context, tenantID, flowID string, version int) (FlowDefinition, error)

	// ListVersions returns every version of tenantID's flowID, oldest
	// first.
	ListVersions(ctx context.Context, tenantID, flowID string) ([]FlowDefinition, error)

	// List returns the active version of every flow belonging to tenantID,
	// newest first.
	List(ctx context.Context, tenantID string) ([]FlowDefinition, error)

	// Activate marks version as flowID's active version — e.g. to roll back
	// to (or forward to) a version other than the most recently saved one.
	// version must already exist for tenantID's flowID (ErrNotFound
	// otherwise).
	Activate(ctx context.Context, tenantID, flowID string, version int) error
}

// newID returns a random 12-byte hex id, matching tenant.newID/session.newID's
// convention (unexported in their own packages, so not reused directly).
func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
