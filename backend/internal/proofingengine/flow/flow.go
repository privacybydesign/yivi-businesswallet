// Package flow holds an org's identification flows as versioned, validated
// objects: the steps a session walks through, the document types and issuing
// countries it accepts, the checks and assurance level it requires, and the
// BSN and redaction policy for its sessions.
//
// A saved FlowDefinition is immutable: editing one creates a new Version, and
// a session keeps the version it was created on (session.Session.FlowVersion),
// so editing a flow never changes the rules a running or finished session is
// judged by.
package flow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"golang.org/x/text/language"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
)

// ErrNotFound: no such flow, or version of one, for the tenant.
var ErrNotFound = errors.New("flow: not found")

// Step is one stage of a flow (FlowDefinition.Steps).
type Step string

const (
	// StepDocumentCapture is the MRZ the Idem app scans to open the chip; always
	// together with StepNFCRead. There is no OCR here.
	StepDocumentCapture Step = "document_capture"
	// StepNFCRead is the ICAO 9303 chip read in the Idem app.
	StepNFCRead Step = "nfc_read"
	// StepDocumentPhoto is a photo of the printed data page, taken in the Idem app
	// and released as documentImage. It stands on its own, with or without the
	// chip read.
	StepDocumentPhoto Step = "document_photo"
	// StepFaceVerification is the whole live face check: capture, liveness and
	// match in one stage.
	StepFaceVerification Step = "face_verification"
	// StepSelfie is the live selfie capture.
	StepSelfie Step = "selfie"
	// StepLiveness is the liveness check against the selfie.
	StepLiveness Step = "liveness"
	// StepFaceMatch is the selfie-vs-document/chip-photo match.
	StepFaceMatch Step = "face_match"
)

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

// StepLocation names who runs the face step: the browser or the Idem app.
// The chip read is always the app's: Web NFC cannot talk to an ICAO chip.
type StepLocation string

const (
	// LocationBrowser, also the empty value: the browser runs the step.
	LocationBrowser StepLocation = "browser"
	// LocationNative: the Idem app runs it, posting to the same step endpoints.
	// It only decides which client does what.
	LocationNative StepLocation = "native"
)

func ValidStepLocation(l StepLocation) bool {
	switch l {
	case "", LocationBrowser, LocationNative:
		return true
	default:
		return false
	}
}

// EffectiveSelfieLocation is SelfieLocation, browser when empty.
func (fd FlowDefinition) EffectiveSelfieLocation() StepLocation {
	if fd.SelfieLocation == "" {
		return LocationBrowser
	}
	return fd.SelfieLocation
}

// FaceProvider names what verifies the face step. Empty is the deployment's
// default.
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

// Check names a proofing check (FlowDefinition.RequiredChecks).
type Check string

const (
	CheckMRZParse             Check = "mrz.parse"
	CheckMRZCheckdigits       Check = "mrz.checkdigits"
	CheckVIZOCR               Check = "viz.ocr" // VIZ is not done, we do capture whole document
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

var validChecks = map[Check]bool{
	CheckMRZParse: true, CheckMRZCheckdigits: true, CheckVIZOCR: true, CheckVIZMRZCrossmatch: true,
	CheckDocumentTemplate: true, CheckDocumentTamper: true, CheckDocumentScreenReplay: true,
	CheckNFCPassiveAuth: true, CheckNFCChipAuth: true, CheckFaceMatch: true, CheckFaceLiveness: true,
	CheckChipVIZCrossmatch: true,
}

// ValidCheck reports whether c is a known Check.
func ValidCheck(c Check) bool { return validChecks[c] }

// thresholdableChecks may have a CheckThresholds entry.
var thresholdableChecks = map[Check]bool{
	CheckFaceMatch: true,
}

// ThresholdSupported reports whether c is a Check that CheckThresholds may
// set a numeric threshold for.
func ThresholdSupported(c Check) bool { return thresholdableChecks[c] }

// checkStepBindings are the steps a check is computed from. Validate holds
// both directions: a step without its mandatory check (nfc_read needs
// nfc.passive_auth, a face match needs face.match) and a check without any of
// its steps are refused. Checks not listed are computed by nothing yet.
var checkStepBindings = []struct {
	check      Check
	anyOfSteps []Step
}{
	{CheckNFCPassiveAuth, []Step{StepNFCRead}},
	{CheckNFCChipAuth, []Step{StepNFCRead}},
	{CheckFaceMatch, []Step{StepFaceVerification, StepFaceMatch}},
	// Liveness comes with every face capture, so any face step provides it.
	{CheckFaceLiveness, []Step{StepFaceVerification, StepSelfie, StepLiveness, StepFaceMatch}},
}

// AssuranceLevel is an eIDAS level of assurance.
type AssuranceLevel string

const (
	AssuranceLevelLow         AssuranceLevel = "low"
	AssuranceLevelSubstantial AssuranceLevel = "substantial"
	AssuranceLevelHigh        AssuranceLevel = "high" // nothing reaches this yet
)

// ValidAssuranceLevel reports whether l is known; "" is no requirement.
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
//   - low: the subject holds genuine evidence: a chip read whose Passive
//     Authentication verifies (SOD signature, data-group hashes, CSCA chain).
//   - substantial: low, the chip proven original (Active Authentication), and
//     the subject bound to it: a live (liveness) face matched by Regula
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

// MeetsLevel reports whether an achieved level satisfies a required one. No
// requirement is always met; nothing achieved meets a requirement, and a
// level this service cannot claim is never met (fail closed).
func MeetsLevel(achieved, required AssuranceLevel) bool {
	if required == "" {
		return true
	}
	want := slices.IndexFunc(LevelRequirements, func(r LevelRequirement) bool { return r.Level == required })
	got := slices.IndexFunc(LevelRequirements, func(r LevelRequirement) bool { return r.Level == achieved })
	return want >= 0 && got >= want
}

// icaoIssuingCodes are the ICAO 9303 issuing-state codes that are no ISO
// 3166-1 country: organisations issuing travel documents, Kosovo, and the
// British nationality variants a passport can be issued under.
var icaoIssuingCodes = []string{
	"EUE",               // European Union (laissez-passer)
	"UNO", "UNA", "UNK", // United Nations and its agencies
	"XOM", "XPO", "XCC", "XES", "XMP", // Order of Malta, Interpol, Caribbean Community, OECS, Mercosur
	"RKS",                             // Kosovo
	"GBD", "GBN", "GBO", "GBP", "GBS", // British dependent territories, overseas, protected and subject citizens
}

// germanyMRZCode is how a German document writes its issuing state: the one
// ICAO 9303 exception to 3-letter codes. It stands for DEU.
const germanyMRZCode = "D"

// ValidIssuingCountry reports whether code is a 3-letter ICAO 9303 issuing
// state: an ISO 3166-1 alpha-3 country (NLD, DEU) or one of icaoIssuingCodes.
// A 2-letter code, lower case, or a code no country has is not one.
func ValidIssuingCountry(code string) bool {
	if len(code) != 3 || strings.ToUpper(code) != code {
		return false
	}
	if slices.Contains(icaoIssuingCodes, code) {
		return true
	}
	region, err := language.ParseRegion(code)
	return err == nil && region.IsCountry() && region.ISO3() == code
}

// IssuingStateCode is the 3-letter code a document's issuing state stands
// for: a German document's "D" is DEU, any other code is itself.
func IssuingStateCode(documentCode string) string {
	if strings.TrimRight(documentCode, "<") == germanyMRZCode {
		return "DEU"
	}
	return documentCode
}

// FlowDefinition is one version of an org's flow. The json tags are the Go
// field names every stored version uses: renaming a field keeps its tag, or
// older versions would decode it empty.
type FlowDefinition struct {
	// ID is the flow's across all its versions, set by Store.Save on a new flow.
	ID       string `json:"ID"`
	TenantID string `json:"TenantID"`
	// Version starts at 1 and goes up by one with every save of the flow.
	Version int    `json:"Version"`
	Name    string `json:"Name"`
	// Steps are the flow's steps in order, as the app gets them.
	Steps []Step `json:"Steps"`
	// RequestedAttributes are the result attributes released, independent of the
	// steps. Empty releases everything the steps collect; ["outcome_only"]
	// releases the outcome only. A stored version's former
	// RequestedAttributesConfigured key is ignored.
	RequestedAttributes []string `json:"RequestedAttributes"`
	// SelfieLocation is who runs the face step (empty is the browser); Validate
	// refuses it without a face step.
	SelfieLocation StepLocation `json:"SelfieLocation"`
	// FaceProvider is the face verifier; empty is the deployment default. Regula
	// needs SelfieLocation native: only the Idem app runs a Regula liveness
	// session.
	FaceProvider FaceProvider `json:"FaceProvider"`
	// AcceptedDocumentTypes are the document codes accepted ("P", ...); empty
	// accepts any. Validate refuses a driving licence (DrivingLicence).
	AcceptedDocumentTypes []string `json:"AcceptedDocumentTypes"`
	// AcceptedIssuingCountries are the 3-letter ICAO issuing states accepted
	// (ValidIssuingCountry); empty accepts any.
	AcceptedIssuingCountries []string `json:"AcceptedIssuingCountries"`
	// RequiredChecks are the checks the flow's steps perform (the stored key keeps
	// its old name): the Idem app runs Active Authentication only when
	// CheckNFCChipAuth is listed (Regula liveness runs either way), a session is scored
	// on them. The eIDAS level follows the evidence produced, not this list, and
	// listing a check decides no outcome: the engine rejects a tampered or cloned chip, a
	// face step that did not verify the person, and an achieved level below
	// RequiredAssuranceLevel.
	//
	// A step's mandatory check must be listed (CheckNFCPassiveAuth with nfc_read,
	// CheckFaceMatch with a face match), and a check needs one of its steps
	// (checkStepBindings). Only the NFC and face checks are computed; any other
	// check listed scores as failed.
	RequiredChecks []Check `json:"RequiredChecks"`
	// CheckThresholds are minimum scores for listed checks; only CheckFaceMatch
	// has a score. A check without one is pass/fail.
	CheckThresholds map[Check]float64 `json:"CheckThresholds"`
	// RequiredAssuranceLevel is the minimum eIDAS level a session must achieve
	// to be approved (MeetsLevel); empty requires none. It changes no step, no
	// collection and no check, and the achieved level is computed without it.
	// Validate refuses one the flow's checks cannot reach.
	RequiredAssuranceLevel AssuranceLevel `json:"RequiredAssuranceLevel"`
	// BSNPolicy, BlurFace and BlurBSN override the defaults one by one, each only
	// when set (empty or nil inherits), so setting BlurBSN alone does not force
	// BlurFace off.
	BSNPolicy privacy.BSNPolicy `json:"BSNPolicy"`
	BlurFace  *bool             `json:"BlurFace"`
	BlurBSN   *bool             `json:"BlurBSN"`
	// RetentionOverride, when set, is how long the flow's finished sessions are
	// kept, replacing the customer's retention and the engine default. A session
	// stamps it at creation, so a later edit does not change it.
	RetentionOverride time.Duration `json:"RetentionOverride"`
	// AssuranceTiers override DefaultAssuranceTiers: the percentage-of-checks
	// ladder a session's achieved score maps onto, as opposed to the eIDAS level
	// RequiredAssuranceLevel demands. Empty uses the default.
	AssuranceTiers []AssuranceTier `json:"AssuranceTiers"`
	// LegalBasis and ProcessingPurpose, when set, override the defaults for the
	// flow's sessions, each independently.
	LegalBasis        privacy.LegalBasis `json:"LegalBasis"`
	ProcessingPurpose string             `json:"ProcessingPurpose"`
	// Active marks the version Store.Get(..., 0) and Store.List return; one per
	// flow.
	Active    bool      `json:"Active"`
	CreatedAt time.Time `json:"CreatedAt"`
}

// AssuranceTier is one rung of a percentage-to-level ladder.
type AssuranceTier struct {
	// Level is a free-form name ("high", "gold"), not an eIDAS level.
	Level string `json:"level"`
	// MinPercent is the minimum share of checks passed (0..1, inclusive).
	MinPercent float64 `json:"minPercent"`
}

// DefaultAssuranceTiers is the ladder for a flow without its own.
var DefaultAssuranceTiers = []AssuranceTier{
	{Level: "perfect", MinPercent: 1},
	{Level: "very_high", MinPercent: 0.8},
	{Level: "high", MinPercent: 0.7},
	{Level: "medium", MinPercent: 0.5},
	{Level: "low", MinPercent: 0.3},
	{Level: "super_low", MinPercent: 0},
}

// EffectiveAssuranceTiers is fd's tiers, else DefaultAssuranceTiers.
func (fd FlowDefinition) EffectiveAssuranceTiers() []AssuranceTier {
	if len(fd.AssuranceTiers) == 0 {
		return DefaultAssuranceTiers
	}
	return fd.AssuranceTiers
}

// LevelForScore is the Level of the tier with the highest MinPercent score
// clears; tiers need not be sorted. "super_low" when none does (Validate
// requires a 0 catch-all, which a flow bypassing it may lack).
func LevelForScore(tiers []AssuranceTier, score float64) string {
	best, bestPercent := "super_low", -1.0
	for _, t := range tiers {
		if score >= t.MinPercent && t.MinPercent > bestPercent {
			best, bestPercent = t.Level, t.MinPercent
		}
	}
	return best
}

// day is a calendar day.
const day = 24 * time.Hour

// MaxRetentionOverride is the longest a flow keeps its finished sessions: a
// year, the longest data retention a customer can choose. It also keeps the
// override within the store's INTEGER seconds.
const MaxRetentionOverride = MaxRetentionOverrideDays * day

// MaxRetentionOverrideDays is MaxRetentionOverride in days.
const MaxRetentionOverrideDays = 365

// DocumentTypeDrivingLicence is an EU driving licence's document type, which
// has no MRZ document code; the app reports it at document_capture. The
// engine refuses a licence at document_capture and nfc_read until it has a
// CSCA source for licences, so a flow may not accept one either.
const DocumentTypeDrivingLicence = "drivers_license"

// DocumentTypeEUDrivingLicence is the other spelling of an EU driving
// licence: mrtdEvidence's documentType for a licence chip.
const DocumentTypeEUDrivingLicence = "eu_driving_licence"

// DrivingLicence is whether t names an EU driving licence, in either
// spelling: a document's or chip access key's type (DocumentTypeDrivingLicence)
// or mrtdEvidence's documentType (DocumentTypeEUDrivingLicence).
func DrivingLicence(t string) bool {
	return t == DocumentTypeDrivingLicence || t == DocumentTypeEUDrivingLicence
}

// ValidationError is Validate refusing a flow definition, as opposed to a
// store failure: the message says which rule.
type ValidationError struct{ err error }

func (e *ValidationError) Error() string { return e.err.Error() }

func (e *ValidationError) Unwrap() error { return e.err }

// Validate refuses a flow definition that is not structurally sound, before
// Store.Save stores it, so a bad flow never fails mid-session. A refusal is a
// *ValidationError.
func Validate(fd FlowDefinition) error {
	if err := validate(fd); err != nil {
		return &ValidationError{err: err}
	}
	return nil
}

func validate(fd FlowDefinition) error {
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

	// document_capture and nfc_read come together: the Idem app scans the MRZ to
	// open the chip and sends both at once.
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

	// A face match without the chip read is valid: it matches against the
	// customer's reference photo, which CreateSession requires per session.
	if err := noDuplicateStrings("accepted document type", fd.AcceptedDocumentTypes); err != nil {
		return err
	}
	if i := slices.IndexFunc(fd.AcceptedDocumentTypes, DrivingLicence); i >= 0 {
		return fmt.Errorf("flow: accepted document type %q is not supported yet: the engine refuses an EU driving licence, having no CSCA source to verify its chip", fd.AcceptedDocumentTypes[i])
	}
	for _, c := range fd.AcceptedIssuingCountries {
		if !ValidIssuingCountry(c) {
			return fmt.Errorf("flow: %q is not an issuing country: use the 3-letter ICAO 9303 code, such as NLD or DEU", c)
		}
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

	// nfc.passive_auth is mandatory with nfc_read: it is the chip's authenticity,
	// which every chip read can compute, and without it a chip flow would never
	// score it.
	if seenSteps[StepNFCRead] && !seenChecks[CheckNFCPassiveAuth] {
		return errors.New("flow: nfc_read requires requiredChecks to include nfc.passive_auth (mandatory for every nfc_read step - nfc.chip_auth remains optional)")
	}

	// face.match is mandatory with a face match, which binds the person to the
	// evidence. Capture or liveness alone, without a comparison, needs no match.
	needsFaceMatch := seenSteps[StepFaceVerification] || seenSteps[StepFaceMatch]
	if needsFaceMatch && !seenChecks[CheckFaceMatch] {
		return errors.New("flow: face_verification requires requiredChecks to include face.match (mandatory whenever face_verification/face_match is a step)")
	}

	// A check without any step that computes it is a configuration mistake (for
	// example nfc.chip_auth without nfc_read).
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
	if fd.RetentionOverride < 0 || fd.RetentionOverride > MaxRetentionOverride {
		return fmt.Errorf("flow: retentionOverride must be between 0 and %d days", MaxRetentionOverride/day)
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

// validateAssuranceTiers refuses a malformed ladder, one without a 0
// catch-all included.
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

// Store keeps the orgs' versioned flow definitions (PostgresStore).
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

	// Activate marks version as flowID's active version, e.g. to roll back
	// to (or forward to) a version other than the most recently saved one.
	// version must already exist for tenantID's flowID (ErrNotFound
	// otherwise).
	Activate(ctx context.Context, tenantID, flowID string, version int) error
}

// newID returns a random 12-byte hex id, like session ids.
func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
