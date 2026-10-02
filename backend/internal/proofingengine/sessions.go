// The session model's shared parts: the app's view of a session and its
// long-poll, the legacy single-shot result for a session without a flow,
// result building (data minimisation, BSN policy, redaction), chip
// verification and assurance scoring. The relying-party side is rp.go; the
// app authenticates with the session token in the path plus its device
// token (device_access.go).
package proofingengine

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/bsn"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/images"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/mrtdverify"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/redact"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// Session events: each is logged (auditProofing), and the ones the wallet
// reconciles on also notify it (notifyEventTypes). The org-facing audit
// trail is the wallet's own, written as it reconciles.
const (
	eventSessionCreated = "proofing.session.created"
	eventSessionOpened  = "proofing.session.opened"
	// eventSessionInProgress is appended once per step, the moment the user
	// starts it (details["stage"] names the step - see markStepStarted), not
	// when its evidence is submitted: that is eventResultSubmitted's job.
	eventSessionInProgress = "proofing.session.in_progress"
	eventSessionCancelled  = "proofing.session.cancelled"

	eventSessionExpired    = "proofing.session.expired"
	eventSessionPurged     = "proofing.session.purged"
	eventResultSubmitted   = "proofing.result.submitted"
	eventResultVerified    = "proofing.result.verified"
	eventResultRejected    = "proofing.result.rejected"
	eventResultNeedsReview = "proofing.result.needs_review"
)

// eventTypeForStatus maps a terminal (or needs_review) session status to the
// audit event type for reaching it via a submitted result. Only called with
// statuses appResultRequest already validated, so the empty default is
// unreachable in practice.
func eventTypeForStatus(status session.Status) string {
	switch status {
	case session.StatusApproved:
		return eventResultVerified
	case session.StatusRejected:
		return eventResultRejected
	case session.StatusNeedsReview:
		return eventResultNeedsReview
	case session.StatusCancelled:
		return eventSessionCancelled
	default:
		return ""
	}
}

// auditStepSubmitted records one step's evidence landing as
// eventResultSubmitted - not as another in_progress event: the step's
// in_progress was already recorded when it started (markStepStarted), so
// repeating it at submission would only restate what's known.
// completedSteps/remainingSteps/nextStep (in fd's step order) say where the
// session now stands.
func (s *Server) auditStepSubmitted(sess session.Session, fd *flow.FlowDefinition, stage string, extra map[string]any) {
	s.auditProofing(sess, eventResultSubmitted, stepAuditDetails(sess, fd, stage, "submitted", extra))
}

// auditStepStarted appends the eventSessionInProgress for the user beginning
// stage (see markStepStarted) - one per step, so the audit log shows the
// session progressing step by step, each with its own in_progress row.
func (s *Server) auditStepStarted(sess session.Session, fd *flow.FlowDefinition, stage string, extra map[string]any) {
	inProgress := sess
	inProgress.Status = session.StatusInProgress
	s.auditProofing(inProgress, eventSessionInProgress, stepAuditDetails(sess, fd, stage, "started", extra))
}

// stepAuditDetails is the per-step audit details: details["stage"] is the
// step, stepState "started" or "submitted", plus the flow progress.
func stepAuditDetails(sess session.Session, fd *flow.FlowDefinition, stage, stepState string, extra map[string]any) map[string]any {
	details := map[string]any{"stage": stage, "stepState": stepState}
	if fd != nil {
		completed, remaining := flowStepProgress(sess, fd)
		details["completedSteps"], details["remainingSteps"] = completed, remaining
		if len(remaining) > 0 {
			details["nextStep"] = remaining[0]
		}
	}
	maps.Copy(details, extra)
	return details
}

// ---- sandbox scripted outcomes ---------------------------------------------
//
// requirements.md §7: "Sandbox mode is a tenant flag that routes sessions to
// scripted outcomes (approve, reject:<code>, needs_review, expire)" - lets a
// relying party test their own integration (status polling, webhook
// handling, result rendering) against every outcome deterministically,
// without a real document/vcmrtd device. Only ever honoured for a tenant
// with Tenant.Sandbox set (see tenantIsSandbox) - resolveScriptedOutcome
// itself doesn't check that, its caller (handleCreateSession) does, before
// the session is even created.

// scriptedOutcome is a parsed createSessionRequest.ScriptedOutcome - see
// parseScriptedOutcome.
type scriptedOutcome struct {
	status    session.Status
	errorCode string
}

// parseScriptedOutcome parses the scriptedOutcome request field:
// "approve", "reject:<code>", "needs_review", or "expire". Unlike a real
// submission's status (session.StatusApproved etc., asserted by the app and
// checked against session.transitions), this is caller-facing wire syntax
// specific to sandbox mode, so it gets its own parser rather than reusing
// session.Status directly.
func parseScriptedOutcome(raw string) (scriptedOutcome, error) {
	switch {
	case raw == "approve":
		return scriptedOutcome{status: session.StatusApproved}, nil
	case raw == "needs_review":
		return scriptedOutcome{status: session.StatusNeedsReview}, nil
	case raw == "expire":
		return scriptedOutcome{status: session.StatusExpired}, nil
	case strings.HasPrefix(raw, "reject:"):
		code := strings.TrimPrefix(raw, "reject:")
		if code == "" {
			return scriptedOutcome{}, fmt.Errorf(`scriptedOutcome "reject:" needs an error code, e.g. "reject:DOC_EXPIRED"`)
		}
		return scriptedOutcome{status: session.StatusRejected, errorCode: code}, nil
	default:
		return scriptedOutcome{}, fmt.Errorf(`unknown scriptedOutcome %q; want "approve", "reject:<code>", "needs_review", or "expire"`, raw)
	}
}

// sandboxFixtureDocument is the one canned "fixture document"
// (requirements.md §7) every scripted outcome but "expire" attaches to its
// result, so a relying party integrating against a sandbox tenant sees the
// same document shape (buildResult's documentInfo) a real submission would.
// IssuingState/Nationality "UTO" is ICAO 9303's own reserved test/training
// country code ("Utopia"), not a real one - deliberately unmistakable as
// fixture data, the same reasoning mrtdtestfixtures uses a fixed test
// document rather than an invented "real-looking" one. An expired session
// never gets this far in a real flow (there was nothing to submit), so
// "expire" carries no result at all - see resolveScriptedOutcome.
func sandboxFixtureDocument() documentInfo {
	valid := true
	return documentInfo{
		Type:         "P",
		Number:       "SANDBOX0000000",
		IssuingState: "UTO",
		Nationality:  "UTO",
		FirstName:    "Sandbox",
		LastName:     "Testperson",
		DisplayName:  "Sandbox Testperson",
		Sex:          "X",
		DateOfBirth:  "1990-01-01",
		DateOfExpiry: "2099-01-01",
		Validity: &documentValidityInfo{
			DocumentNumberCheckDigitValid: &valid,
			DateOfBirthCheckDigitValid:    &valid,
			DateOfExpiryCheckDigitValid:   &valid,
			CompositeCheckDigitValid:      &valid,
			NotExpired:                    &valid,
		},
	}
}

// effectivePrivacyPolicy is the BSN/redaction policy that governs a session:
// the flow's, where it sets one, else the default (BSN kept, no blurring).
func effectivePrivacyPolicy(resolvedFlow *flow.FlowDefinition) (privacy.BSNPolicy, privacy.RedactionPolicy) {
	bsnPolicy, redaction := privacy.BSNPolicyRetrieve, privacy.RedactionPolicy{}
	if resolvedFlow != nil {
		if resolvedFlow.BSNPolicy != "" {
			bsnPolicy = resolvedFlow.BSNPolicy
		}
		if resolvedFlow.BlurFace != nil {
			redaction.BlurFace = *resolvedFlow.BlurFace
		}
		if resolvedFlow.BlurBSN != nil {
			redaction.BlurBSN = *resolvedFlow.BlurBSN
		}
	}
	return bsnPolicy, redaction
}

// resolveSessionFlow looks up the exact flow version sess.Flow/
// sess.FlowVersion was pinned to at creation (see session.Session.
// FlowVersion's doc comment) - never "whichever version is active now", so a
// flow definition edited (or rolled back/forward) after a session started
// never changes what that session is judged against. Returns (nil, nil),
// not an error, whenever there's simply no flow governing this session: no
// flow.Store configured, or FlowVersion == 0 (the session predates flow
// resolution, or its Flow name — e.g. the "default" default, see Session.
// Flow — never matched a stored flow definition at creation time).
func (s *Server) resolveSessionFlow(ctx context.Context, sess session.Session) (*flow.FlowDefinition, error) {
	if s.flows == nil || sess.FlowVersion == 0 {
		return nil, nil
	}
	fd, err := s.flows.Get(ctx, sess.TenantID, sess.Flow, sess.FlowVersion)
	if errors.Is(err, flow.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &fd, nil
}

// attributesForSteps derives a session's RequestedAttributes from a resolved
// flow definition's Steps — this is "the session engine executes a flow
// definition" for data collection specifically: which attributes end up
// requested is computed from Steps (data), never a hard-coded per-method
// list, and a relying party creating a session against a flow no longer
// picks requestedAttributes itself (see handleCreateSession).
func attributesForSteps(steps []flow.Step) []string {
	var attrs []string
	add := func(vs ...string) {
		for _, v := range vs {
			if !slices.Contains(attrs, v) {
				attrs = append(attrs, v)
			}
		}
	}
	for _, step := range steps {
		switch step {
		case flow.StepDocumentCapture:
			add(attrDocument, attrDocumentImage)
		case flow.StepNFCRead:
			add(attrDG11, attrDG2, attrChipChecks)
		case flow.StepDocumentPhoto:
			add(attrDocumentImage)
		case flow.StepSelfie:
			add(attrSelfie)
		case flow.StepLiveness, flow.StepFaceMatch:
			add(attrBiometrics)
		case flow.StepFaceVerification:
			add(attrSelfie, attrBiometrics)
		}
	}
	return attrs
}

// flowComplianceFailure reports whether req's submission is outright outside
// what fd can accept as complete evidence for a session - eligibility and
// evidence-completeness, not assurance: whether an individual RequiredChecks
// entry (nfc.passive_auth, nfc.chip_auth, face.match, face.liveness)
// actually passed no longer affects session success at all - see
// assuranceInfo/computeAssurance - it only affects how high the resulting
// assurance score/eIDAS level lands. A tampered or cloned chip is a
// separate, harder failure (authenticityFailure), not enforced here. fd ==
// nil (no flow governs this session) never fails.
//
// sess is used for exactly one thing: a flow whose Steps include the
// selfie/liveness/face_match cluster requires real, server-computed
// evidence for it - sess.Steps.Selfie, set only by this server's own
// POST .../steps/selfie computation (or, on the older single-shot endpoint,
// selfieEvidenceFromResult recomputing it from a submitted image). Without
// this, a session could reach StatusApproved on the older POST .../result
// endpoint with a bare self-reported Biometrics claim and no image ever
// submitted at all - not a failed check (there's nothing to even evaluate),
// but the step never actually completing, which does still block success
// the same way a missing document_capture/nfc_read would (docs/compliance.md
// §2). finishSession (the step-based path) never reaches this
// call until requiredStepsComplete already confirmed sess.Steps.Selfie is
// set, so this only ever bites the older POST .../result.
func flowComplianceFailure(fd *flow.FlowDefinition, sess session.Session, req appResultRequest) (failed bool, errorCode string) {
	if fd == nil {
		return false, ""
	}
	if req.Document != nil {
		if len(fd.AcceptedDocumentTypes) > 0 && !slices.Contains(fd.AcceptedDocumentTypes, req.Document.Type) {
			return true, "DOCUMENT_TYPE_NOT_ACCEPTED"
		}
		if len(fd.AcceptedIssuingCountries) > 0 && !slices.Contains(fd.AcceptedIssuingCountries, req.Document.IssuingState) {
			return true, "DOCUMENT_COUNTRY_NOT_ACCEPTED"
		}
	}
	hasFaceStep := slices.Contains(fd.Steps, flow.StepFaceVerification) || slices.Contains(fd.Steps, flow.StepSelfie) ||
		slices.Contains(fd.Steps, flow.StepLiveness) || slices.Contains(fd.Steps, flow.StepFaceMatch)
	if hasFaceStep && sess.Steps.Selfie == nil {
		return true, "FACE_STEP_NOT_COMPLETED"
	}
	return false, ""
}

// resolveScriptedOutcome immediately drives a freshly created sandbox
// session to outcome, synchronously, as part of the create-session request -
// there is no real app/device to wait for. sess.Status is always
// StatusCreated (this is only ever called right after Create): for every
// outcome but "expire" it walks through the same intermediate states
// (opened, in_progress) and audit events handleAppSessionResult would for a
// real submission, so the audit trail and any webhook look the same, just
// synchronous; "expire" instead transitions directly to expired, since a
// real expiry never has a result to submit either.
func (s *Server) resolveScriptedOutcome(sess session.Session, fd *flow.FlowDefinition, outcome scriptedOutcome) (session.Session, error) {
	now := time.Now().UTC()
	var result map[string]any
	if outcome.status != session.StatusExpired {
		result = map[string]any{"document": sandboxFixtureDocument()}
	}
	if outcome.status == session.StatusApproved {
		result["assurance"] = sandboxAssurance(fd)
	}
	updated, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		if outcome.status == session.StatusExpired {
			return sess.SetStatus(session.StatusExpired, now)
		}
		if err := sess.SetStatus(session.StatusOpened, now); err != nil {
			return err
		}
		if err := sess.SetStatus(session.StatusInProgress, now); err != nil {
			return err
		}
		if err := sess.SetStatus(outcome.status, now); err != nil {
			return err
		}
		sess.ErrorCode = outcome.errorCode
		sess.Result = result
		return nil
	})
	if err != nil {
		return session.Session{}, err
	}
	if outcome.status == session.StatusExpired {
		s.auditProofing(updated, eventSessionExpired, map[string]any{"reason": "scripted", "sandbox": true})
		return updated, nil
	}
	s.auditProofing(updated, eventSessionOpened, map[string]any{"sandbox": true})
	s.auditProofing(updated, eventResultSubmitted, map[string]any{"stage": "app_result", "claimedStatus": string(outcome.status), "sandbox": true})
	outcomeDetails := map[string]any{"sandbox": true}
	if updated.ErrorCode != "" {
		outcomeDetails["errorCode"] = updated.ErrorCode
	}
	s.auditProofing(updated, eventTypeForStatus(updated.Status), outcomeDetails)
	return updated, nil
}

// sandboxAssurance is a scripted approval's assurance: the eIDAS level its
// flow requires, so a sandbox approval meets a relying party's own level check
// the way a real approval would; none when the flow requires none, like a real
// session (computeEIDASAssuranceLevel).
func sandboxAssurance(fd *flow.FlowDefinition) assuranceInfo {
	level, tiers := flow.AssuranceLevel(""), flow.DefaultAssuranceTiers
	if fd != nil {
		tiers = fd.EffectiveAssuranceTiers()
		level = fd.RequiredAssuranceLevel
	}
	return assuranceInfo{Level: flow.LevelForScore(tiers, 1), Score: 1, ChecksPassed: 1, ChecksTotal: 1, EIDASLevel: level}
}

// ---- payloads -------------------------------------------------------------

// appSessionView is what the app-facing GET returns: only what it needs to
// run the flow and present it to the user, nothing about the tenant that
// isn't already meant to be shown. RelyingParty is the tenant's display
// Name (tenantDisplayName) — a relying party's own API key/tenant id is
// never shown to the applicant's device, only a human-readable name.
type appSessionView struct {
	ID           string         `json:"id"`
	Method       session.Method `json:"method"`
	RelyingParty string         `json:"relyingParty"`
	// Status lets the browser hosted flow (requirements.md §1) tell a
	// finished session (approved/rejected/needs_review/expired/cancelled)
	// apart from one still in progress, without needing the relying-party
	// -facing sessionView (which the app-facing token can't authenticate
	// for anyway).
	Status session.Status `json:"status"`
	// Language is the shipped language ("en" or "nl", see internal/i18n)
	// the browser flow should render in: the relying party's choice at
	// session creation (session.Session.Language) when that names a shipped
	// one, else the caller's own Accept-Language, else "en". Always set.
	Language            string   `json:"language,omitempty"`
	RequestedAttributes []string `json:"requestedAttributes,omitempty"`
	// Steps is the resolved flow definition's step order (see flow.
	// FlowDefinition.Steps) — this, not any hard-coded order in this
	// server's code, is what tells the app what to do and in what
	// sequence. Empty when no flow definition governs this session (see
	// resolveSessionFlow), matching every client that predates flow
	// definitions.
	Steps []flow.Step `json:"steps,omitempty"`
	// SelfieLocation resolves flow.FlowDefinition.SelfieLocation to its
	// effective value ("browser" or "native") — read by both the browser
	// page and vcmrtd off this same call to decide which of them performs
	// the selfie/liveness/face_match cluster. Always "browser" when no
	// flow governs this session (matching every client that predates this
	// field). document_capture and nfc_read have no equivalent: they're
	// always native, never surfaced here as a choice.
	SelfieLocation flow.StepLocation `json:"selfieLocation"`
	ExpiresAt      time.Time         `json:"expiresAt"`
	// AAChallenge is the RND.IFD the app must send to the chip's INTERNAL
	// AUTHENTICATE command if it performs Active Authentication — see
	// session.Session.AAChallenge. Evidence submitted with a different nonce
	// is never trusted as proof of possession, however valid its signature.
	AAChallenge string `json:"aaChallenge,omitempty"`
	// RequiredChecks is every check this session is scored on
	// (assuranceChecksFor): the app runs each one it performs itself -
	// Active/Chip Authentication for nfc.chip_auth - because the flow asks
	// for it, not because a device setting happens to be on. A skipped check
	// scores as failed and caps the eIDAS level (computeEIDASAssuranceLevel).
	RequiredChecks []flow.Check `json:"requiredChecks"`
	// CompletedSteps names which of Steps already has accumulated evidence
	// (see session.Session.CompletedSteps) — this is what lets the browser
	// hosted flow (requirements.md §1) resume to the right screen after a
	// page reload or a QR handover to a different device, instead of
	// starting the session over.
	CompletedSteps []string `json:"completedSteps,omitempty"`
	// NativeHandoff, when present, says vcmrtd still has work in this
	// session: nfc_read and document_capture (always together, always
	// native, if present in Steps), plus the selfie cluster when
	// SelfieLocation says "native" — see nativeHandoffPending. It carries no
	// link: the browser mints the native slot's QR itself
	// (POST .../handover {role: "native"}), which works whether or not a
	// vcmrtd device already holds that slot.
	NativeHandoff *nativeHandoffInfo `json:"nativeHandoff,omitempty"`
	// ResetCount goes up every time the relying party resets the session
	// (POST .../reset, session.Session.ResetCount). A client that sees it
	// change must drop whatever it collected locally and start over from
	// its first step - the server already dropped its copy.
	ResetCount int `json:"resetCount"`
	// ChangeKey is what to pass as ?since= to GET .../events to wait for
	// the next change of this view - see appSessionChangeKey. Clients echo
	// it back rather than rebuilding it, so its format can change freely.
	ChangeKey string `json:"changeKey"`

	// FlowID/FlowVersion name the flow definition governing the session.
	FlowID      string `json:"flowId,omitempty"`
	FlowVersion int    `json:"flowVersion,omitempty"`
	// CurrentStep is the server-defined step to continue with (the first
	// of Steps without a result), "" once the flow is complete. Clients
	// resume from this, never from whatever step they last showed locally.
	// Omitted (null) when no flow governs the session: there is no step
	// model to derive it from, and "" would wrongly read as "complete".
	CurrentStep *string `json:"currentStep,omitempty"`
	// ChipAccess is the MRZ-derived chip access key document_capture
	// stored, present only while CurrentStep is nfc_read, so a device that
	// took over can read the chip without rescanning the MRZ.
	ChipAccess *session.ChipAccessKey `json:"chipAccess,omitempty"`
	// FaceReference is the photo the face step compares against - the chip's
	// DG2 from the nfc_read step, or the relying party's referencePhoto -
	// present only while CurrentStep is the face step and the flow runs it
	// in vcmrtd, so a device that took over can run it without reading the
	// chip itself.
	FaceReference *photoInfo `json:"faceReference,omitempty"`
	// FaceVerification tells vcmrtd to run the face step as a Regula liveness
	// session and submit its livenessTransactionId. Present only when the
	// flow's face provider is Regula (faceProviderFor) and Regula is configured.
	FaceVerification *faceVerificationInfo `json:"faceVerification,omitempty"`
	// FaceProvider is the verifier that scores this session's face step
	// (faceProviderFor), present only when the flow runs that step in
	// vcmrtd: the app runs the matching engine rather than whichever one its
	// own settings name.
	FaceProvider flow.FaceProvider `json:"faceProvider,omitempty"`
	// StepResults are the completed steps' server-side results (verdicts
	// only, no evidence), keyed by step.
	StepResults map[string]stepResultView `json:"stepResults"`
	// Lifecycle is ACTIVE, COMPLETE (every step has a result and the
	// outcome in Status is decided), EXPIRED or CANCELLED.
	Lifecycle string `json:"lifecycle"`
	// ReadyToSubmit: every step has a result but the session has no
	// outcome yet - it waits for the user to submit it (POST .../submit).
	// Show a submit action; nothing finishes the session on its own.
	ReadyToSubmit bool `json:"readyToSubmit,omitempty"`
	// Device is the calling device's own authorization; Devices is every
	// slot's state, so one client can see the other went inactive and
	// offer a handover. See device_access.go.
	Device  callerDeviceView      `json:"device"`
	Devices map[string]deviceView `json:"devices"`
}

// faceVerificationInfo is appSessionView.FaceVerification's shape: the
// Face API to run liveness against and the tag to set on the session.
type faceVerificationInfo struct {
	Provider   string `json:"provider"`
	FaceAPIURL string `json:"faceApiUrl"`
	Tag        string `json:"tag"`
}

// nativeHandoffInfo is appSessionView.NativeHandoff's shape: the slot to
// mint a grant for and whether a device already holds it.
type nativeHandoffInfo struct {
	Role    session.DeviceRole `json:"role"`
	Claimed bool               `json:"claimed"`
}

// appResultRequest is what the app posts back once it has read the chip and
// run its face check: the identity read off the document, the raw chip
// evidence needed to independently verify it, the biometric outcome, and
// device/app metadata. Status is still asserted by the caller rather than
// computed here. Only the parts the relying party asked for end up in the
// stored/returned result — see buildResult/attrRequested.
type appResultRequest struct {
	Status    session.Status `json:"status"`
	ErrorCode string         `json:"errorCode,omitempty"`
	Document  *documentInfo  `json:"document,omitempty"`
	Photo     *photoInfo     `json:"photo,omitempty"`
	// Selfie is the live face capture taken during the app's on-device face
	// verification (or the Iris SDK's), submitted alongside Photo so the two
	// can be shown side by side — see attrSelfie/buildResult.
	Selfie *photoInfo `json:"selfie,omitempty"`
	// DocumentImage is a visual (VIZ) capture of the document itself — see
	// documentImageInfo's doc comment. It is the front; DocumentImageBack is
	// the back, when the document_photo step took one.
	DocumentImage     *documentImageInfo `json:"documentImage,omitempty"`
	DocumentImageBack *documentImageInfo `json:"documentImageBack,omitempty"`
	// ChipChecks, if present with no MrtdEvidence, is accepted for wire
	// compatibility with older clients but never trusted or stored — see
	// buildResult. Send MrtdEvidence instead so chipChecks in the result
	// reflects this server's own verification, not the app's say-so.
	ChipChecks   *chipChecksInfo      `json:"chipChecks,omitempty"`
	MrtdEvidence *mrtdEvidenceRequest `json:"mrtdEvidence,omitempty"`
	Biometrics   *biometricsInfo      `json:"biometrics,omitempty"`
	Device       *deviceInfo          `json:"device,omitempty"`
}

// documentInfo is the identity read off the document's DG1/MRZ, plus DG11
// extras when the document carries them and DG11 was requested. Field
// names/shape follow vcmrtd's own PassportMRZ/PassportData types, not an
// ICAO or ISO standard encoding — treat this as provisional until the
// shared result schema (issue #5) settles it.
type documentInfo struct {
	Type         string `json:"type,omitempty"` // ICAO document code, e.g. "P" for passport
	Number       string `json:"number,omitempty"`
	IssuingState string `json:"issuingState,omitempty"`
	Nationality  string `json:"nationality,omitempty"`
	FirstName    string `json:"firstName,omitempty"`
	LastName     string `json:"lastName,omitempty"`
	// DisplayName prefers the DG11 name (UTF-8, preserves diacritics) over
	// the MRZ name (ICAO-transliterated to basic Latin) when both exist —
	// see PassportData.displayName in vcmrtd.
	DisplayName string `json:"displayName,omitempty"`
	Sex         string `json:"sex,omitempty"`

	DateOfBirth  string `json:"dateOfBirth,omitempty"`  // YYYY-MM-DD
	DateOfExpiry string `json:"dateOfExpiry,omitempty"` // YYYY-MM-DD

	// DG11 extras — present only when the document carries DG11, it was
	// read, and "dg11" was requested (see attrDG11).
	PersonalNumber string `json:"personalNumber,omitempty"`
	PlaceOfBirth   string `json:"placeOfBirth,omitempty"`

	Validity *documentValidityInfo `json:"validity,omitempty"`
}

// documentValidityInfo is the ICAO 9303 MRZ check-digit validation
// (document number, date of birth, date of expiry, and the composite of
// all three) plus a plain "is the printed expiry date in the future" check.
// This validates the numbers printed/encoded on the document against each
// other, not the document's authenticity — that's chipChecksInfo.
type documentValidityInfo struct {
	DocumentNumberCheckDigitValid *bool `json:"documentNumberCheckDigitValid,omitempty"`
	DateOfBirthCheckDigitValid    *bool `json:"dateOfBirthCheckDigitValid,omitempty"`
	DateOfExpiryCheckDigitValid   *bool `json:"dateOfExpiryCheckDigitValid,omitempty"`
	CompositeCheckDigitValid      *bool `json:"compositeCheckDigitValid,omitempty"`
	NotExpired                    *bool `json:"notExpired,omitempty"`
}

// photoInfo is the face image read off the chip's DG2 data group. It is
// kept out of the stored/returned result unless the relying party
// explicitly requested it ("dg2" or "face_image", see attrRequested) —
// requirements.md §8's data minimisation applies even when the app sends it
// unconditionally, so the filtering happens server-side, not by trusting
// the app to withhold it.
type photoInfo struct {
	ImageBase64 string `json:"imageBase64,omitempty"`
	MimeType    string `json:"mimeType,omitempty"` // e.g. "image/jpeg", "image/jp2"
}

// imageRegion is a normalized ([0,1], top-left origin) bounding box within
// an image — currently only used to mark where a documentImageInfo's BSN
// text was located, for internal/redact.Region to blur.
type imageRegion struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// documentImageInfo is a visual (VIZ) capture of the document's data page or
// card — the photographed image, as opposed to photoInfo's DG2 chip
// portrait. Dutch ID cards print the BSN on this page; BSNRegion, when the
// app (or a future viz.ocr check, requirements.md §4) located that text
// within the image, is what tells buildResult where to blur before storage
// when the tenant's redaction policy asks for it (see
// privacy.RedactionPolicy.BlurBSN). A nil BSNRegion means nothing to redact,
// regardless of policy — this server never guesses at where the BSN might
// be printed.
type documentImageInfo struct {
	ImageBase64 string       `json:"imageBase64,omitempty"`
	MimeType    string       `json:"mimeType,omitempty"`
	BSNRegion   *imageRegion `json:"bsnRegion,omitempty"`
}

// mrtdEvidenceRequest carries the raw chip bytes needed for this server to
// independently run Passive Authentication (EF.SOD signature against a CSCA
// trust anchor, plus per-data-group hash verification) and Active/Chip
// Authentication (challenge-response signature against the chip's public
// key) — see internal/mrtdverify. All hex-named fields are hex-encoded.
type mrtdEvidenceRequest struct {
	// EFSOD is the raw EF.SOD (Security Object Document) exactly as read
	// off the chip.
	EFSOD string `json:"efSod"`
	// DataGroups maps "DG1".."DG16" to that data group's raw bytes exactly
	// as read off the chip — not re-derived from parsed fields, since
	// Passive Authentication hashes these bytes and compares against the
	// signed hash list in EFSOD.
	DataGroups map[string]string `json:"dataGroups"`
	// DocumentType picks which Passive Authentication path
	// verifyMrtdEvidence runs — see documentTypeEUDrivingLicence. Empty
	// defaults to the ICAO path (every submission predating this field,
	// including every existing passport/ID-card integration, is ICAO).
	// Deliberately its own explicit field rather than inferred from
	// AAKeyDataGroup=="DG13": an older ICAO passport with no DG15 also has
	// an empty AAKeyDataGroup, which would make that inference ambiguous.
	DocumentType string `json:"documentType,omitempty"`
	// AAKeyDataGroup names which entry in DataGroups carries the Active
	// Authentication public key (tag 0x6F wrapping a SubjectPublicKeyInfo):
	// "DG15" for passports/ID cards, "DG13" for EU driving licences. Empty
	// when the chip doesn't support Active Authentication — but see
	// resolveAAKeyDataGroup: left empty on an ICAO submission that DOES
	// include a "DG15" entry, it defaults to "DG15" rather than being
	// treated as "AA not attempted", since vcmrtd's current wire format
	// (RawDocumentData) has no equivalent field at all and simply includes
	// DG15 in dataGroups when the chip supports AA.
	AAKeyDataGroup string `json:"aaKeyDataGroup,omitempty"`
	// Nonce and AASignature are the Active Authentication challenge sent to
	// the chip and its signed response. Both required when AAKeyDataGroup
	// is set.
	Nonce       string `json:"nonce,omitempty"`
	AASignature string `json:"aaSignature,omitempty"`
}

// DocumentType values for mrtdEvidenceRequest.DocumentType. Empty (or
// "icao") runs mrtdverify.VerifyPassiveICAO: a typed document.Document plus
// gmrtd's passiveauth.PassiveAuth, which additionally cross-checks EF.SOD's
// signing certificate's country against DG1's declared issuing country and
// narrows the trust pool to that country. Passports and ID cards use ICAO
// 9303's DG1/DG2 encoding, which gmrtd's typed parsers can read — this is
// the default.
const (
	// documentTypeEUDrivingLicence runs the generic mrtdverify.VerifyPassive
	// instead: EU driving licences use non-ICAO DG1/DG6/DG13 encodings
	// gmrtd's typed document parsers cannot read at all, so the
	// typed/country-cross-check path above is not available for them —
	// matches go-passport-issuer's own separate, un-country-filtered EDL
	// implementation, which has the same limitation.
	documentTypeEUDrivingLicence = "eu_driving_licence"
)

// resolveAAKeyDataGroup fills in ev.AAKeyDataGroup's default for a client
// that never sets it at all — notably vcmrtd's current wire format
// (RawDocumentData, the Flutter app this server's app-facing API is actually
// built for) has no aaKeyDataGroup field: it just includes a "DG15" entry in
// dataGroups when the chip supports Active Authentication, the same way it
// already includes nonce/aaSignature. Without this default, a real
// submission from that app would silently never trigger Active
// Authentication verification, regardless of a genuine nonce/signature being
// present — an explicit AAKeyDataGroup always wins when the client does set
// one. EU driving licences use DG13, not DG15, and predate any such client
// integration, so the default only applies to the (default) ICAO path.
func resolveAAKeyDataGroup(ev *mrtdEvidenceRequest) string {
	if ev.AAKeyDataGroup != "" {
		return ev.AAKeyDataGroup
	}
	if ev.DocumentType == documentTypeEUDrivingLicence {
		return ""
	}
	if _, ok := ev.DataGroups["DG15"]; ok {
		return "DG15"
	}
	return ""
}

// chipChecksInfo reports the ICAO 9303 chip-authenticity checks: Passive
// Authentication (does the signed Document Security Object match what was
// actually read off the chip, and does the signer chain up to a trusted
// CSCA?) and Active/Chip Authentication (does the chip hold the private key
// it's supposed to, which a cloned chip — data copied without the private
// key — cannot do). Populated only from this server's own verification of
// MrtdEvidence (see verifyMrtdEvidence/internal/mrtdverify) — never from the
// app's own claim, which cannot be trusted to be honest or correct.
type chipChecksInfo struct {
	PassiveAuthentication *passiveAuthInfo `json:"passiveAuthentication,omitempty"`
	ActiveAuthentication  *activeAuthInfo  `json:"activeAuthentication,omitempty"`
	CloneDetected         *bool            `json:"cloneDetected,omitempty"`
	TamperDetected        *bool            `json:"tamperDetected,omitempty"`
}

// passiveAuthInfo: SOD signature verification, per-data-group hash
// matching, and CSCA trust-chain validation.
type passiveAuthInfo struct {
	SODSignatureValid    *bool    `json:"sodSignatureValid,omitempty"`
	DataGroupHashesValid *bool    `json:"dataGroupHashesValid,omitempty"`
	InvalidDataGroups    []string `json:"invalidDataGroups,omitempty"` // e.g. ["DG2"] when a hash mismatched
	CSCATrustChainValid  *bool    `json:"cscaTrustChainValid,omitempty"`
	IssuingCSCA          string   `json:"issuingCsca,omitempty"`
	// DocumentComplete is false when EF.SOD's hash list references DG14 or
	// DG15 (chip/Active Authentication material) that was never submitted at
	// all — see mrtdverify.PassiveResult.DocumentComplete. Always true for
	// documentTypeEUDrivingLicence, which never runs this check.
	DocumentComplete  *bool  `json:"documentComplete,omitempty"`
	DocumentVerifyErr string `json:"documentVerifyErr,omitempty"`
}

// activeAuthInfo: Attempted only says the app engaged AA/CA (which itself
// requires a nonce/challenge from the chip); Passed is the actual
// clone/tamper-resistant proof-of-possession result.
type activeAuthInfo struct {
	Attempted *bool  `json:"attempted,omitempty"`
	Passed    *bool  `json:"passed,omitempty"`
	Method    string `json:"method,omitempty"` // "active_authentication" | "chip_authentication"
}

// biometricsInfo is the face verification outcome: the live capture's
// similarity against the chip's DG2 photo, whether liveness passed, and
// which engine produced the result. FaceMatchScore is a raw similarity
// score (0-1); it doesn't replace FaceVerified — the app's on-device check
// still gates whether a result is reachable at all, this just reports the
// number behind that gate.
type biometricsInfo struct {
	FaceMatchScore *float64 `json:"faceMatchScore,omitempty"`
	FaceVerified   *bool    `json:"faceVerified,omitempty"`
	LivenessResult string   `json:"livenessResult,omitempty"` // "passed" | "failed" | "not_performed"
	// LivenessScore is the real anti-spoof model's own live-class
	// confidence (api.checkLiveness/face.Engine.Liveness — MiniFASNet-v2
	// when provisioned), 0..1, rounded to 3 decimals - nil when the server
	// fell back to the frame-distinctness heuristic instead (no model
	// loaded, or this is bound_login.go's own path, which doesn't run this
	// check at all yet - see LivenessResult's "not_performed").
	LivenessScore *float64 `json:"livenessScore,omitempty"`
	Engine        string   `json:"engine,omitempty"` // "on_device" | "iris" | "ghostfacenet_tflite" (this server) | "regula"

	// Set by this server's own 1:1 verification (biometric-bound login, see
	// bound_login.go), never by an app's self-report: the threshold the score
	// was held to, how many live frames were evaluated, where the reference
	// face came from ("yivi:pbdf.pbdf.passport.photo"), and which frame
	// integrity checks ran.
	Threshold       *float64       `json:"threshold,omitempty"`
	FramesEvaluated *int           `json:"framesEvaluated,omitempty"`
	ReferenceSource string         `json:"referenceSource,omitempty"`
	Injection       *injectionInfo `json:"injection,omitempty"`
}

// deviceInfo is app/device metadata, not part of the identity result, so it
// isn't gated by requestedAttributes the way document/photo/checks are.
type deviceInfo struct {
	AppVersion     string `json:"appVersion,omitempty"`
	DevicePlatform string `json:"devicePlatform,omitempty"` // e.g. "android", "ios"
}

// Attribute keys a relying party can list in requestedAttributes to opt
// into a part of the result — see attrRequested/buildResult. Provisional
// vocabulary, same caveat as documentInfo's shape.
const (
	attrDocument      = "dg1"        // document/MRZ fields, holder identity, validity
	attrDG11          = "dg11"       // DG11 extras: personalNumber, placeOfBirth
	attrDG2           = "dg2"        // raw face image off the chip
	attrFaceImage     = "face_image" // alias for attrDG2
	attrChipChecks    = "chip_checks"
	attrBiometrics    = "biometrics"
	attrSelfie        = "selfie"         // live selfie captured during face verification
	attrDocumentImage = "document_image" // visual (VIZ) capture of the document
)

// attrRequested reports whether any of keys was requested. An empty
// RequestedAttributes list is unrestricted — today's default, and every
// existing integration predates this filtering — only a relying party that
// actually lists attributes gets narrowed down to just those.
func attrRequested(sess session.Session, keys ...string) bool {
	if len(sess.RequestedAttributes) == 0 {
		return true
	}
	for _, want := range keys {
		if slices.Contains(sess.RequestedAttributes, want) {
			return true
		}
	}
	return false
}

// buildResult assembles the stored/returned result from what the app
// posted, keeping only what sess.RequestedAttributes asked for. This is the
// data-minimisation boundary (requirements.md §8): the raw DG2 photo in
// particular is dropped here even if the app sent it, unless attrDG2/
// attrFaceImage was requested. It is also the privacy-policy enforcement
// point (requirements.md §3): BSN masking/omission and image redaction are
// applied here too, so what gets written to Session.Result — and from there
// to storage, the relying party, and webhooks — has already been through
// the effective BSN/redaction policy (effectivePrivacyPolicy), not just
// requestedAttributes. Called
// once, before the result is ever persisted (see handleAppSessionResult); a
// value that leaves this function raw is never redacted downstream.
//
// verifiedChipChecks is this server's own computed result (see
// verifyMrtdEvidence) — req.ChipChecks, the app's self-reported claim, is
// never used here regardless of what the app sent.
func buildResult(sess session.Session, req appResultRequest, verifiedChipChecks *chipChecksInfo, fd *flow.FlowDefinition, bsnPolicy privacy.BSNPolicy, redaction privacy.RedactionPolicy) map[string]any {
	result := map[string]any{}
	if req.Document != nil && attrRequested(sess, attrDocument) {
		doc := *req.Document
		applyBSNPolicy(&doc, bsnPolicy)
		if !attrRequested(sess, attrDG11) {
			doc.PersonalNumber = ""
			doc.PlaceOfBirth = ""
		}
		result["document"] = doc
	}
	if req.Photo != nil && attrRequested(sess, attrDG2, attrFaceImage) {
		photo := *req.Photo
		// DG2 portraits are frequently JPEG2000, which no mainstream browser
		// renders inline (see images.ToDisplayablePNG) — convert so the
		// prove-identity page and demo actually show the photo.
		if converted, mime, err := images.ToDisplayablePNG(photo.ImageBase64, photo.MimeType); err != nil {
			slog.Warn("identity proofing: could not convert the photo for display", slog.String("session_id", sess.ID), slog.Any("error", err))
		} else {
			photo.ImageBase64, photo.MimeType = converted, mime
		}
		released := true
		if redaction.BlurFace {
			if blurred, mime, ok := blurFace(photo.ImageBase64, photo.MimeType); ok {
				photo.ImageBase64, photo.MimeType = blurred, mime
			} else {
				slog.Warn("identity proofing: could not blur the photo; leaving it out", slog.String("session_id", sess.ID))
				released = false
			}
		}
		if released {
			result["photo"] = &photo
		}
	}
	if req.Selfie != nil && attrRequested(sess, attrSelfie) {
		selfie := *req.Selfie
		if converted, mime, err := images.ToDisplayablePNG(selfie.ImageBase64, selfie.MimeType); err != nil {
			slog.Warn("identity proofing: could not convert the selfie for display", slog.String("session_id", sess.ID), slog.Any("error", err))
		} else {
			selfie.ImageBase64, selfie.MimeType = converted, mime
		}
		released := true
		if redaction.BlurFace {
			if blurred, mime, ok := blurFace(selfie.ImageBase64, selfie.MimeType); ok {
				selfie.ImageBase64, selfie.MimeType = blurred, mime
			} else {
				slog.Warn("identity proofing: could not blur the selfie; leaving it out", slog.String("session_id", sess.ID))
				released = false
			}
		}
		if released {
			result["selfie"] = &selfie
		}
	}
	// A face matched against the relying party's own photo (a flow without
	// nfc_read) says so, and releases that photo with the selfie, so whoever
	// reads the result sees both faces that were compared.
	if sess.ReferencePhoto != "" {
		result["faceReference"] = faceReferenceRelyingParty
		if attrRequested(sess, attrSelfie) {
			ref := photoInfo{ImageBase64: sess.ReferencePhoto, MimeType: sess.ReferencePhotoMime}
			released := true
			if redaction.BlurFace {
				if blurred, mime, ok := blurFace(ref.ImageBase64, ref.MimeType); ok {
					ref.ImageBase64, ref.MimeType = blurred, mime
				} else {
					slog.Warn("identity proofing: could not blur the reference photo; leaving it out", slog.String("session_id", sess.ID))
					released = false
				}
			}
			if released {
				result["referencePhoto"] = &ref
			}
		}
	}
	if attrRequested(sess, attrDocumentImage) {
		if req.DocumentImage != nil {
			result["documentImage"] = releasedDocumentImage(sess, *req.DocumentImage, redaction)
		}
		if req.DocumentImageBack != nil {
			result["documentImageBack"] = releasedDocumentImage(sess, *req.DocumentImageBack, redaction)
		}
	}
	if verifiedChipChecks != nil && attrRequested(sess, attrChipChecks) {
		result["chipChecks"] = verifiedChipChecks
	}
	if req.Biometrics != nil && attrRequested(sess, attrBiometrics) {
		result["biometrics"] = req.Biometrics
	}
	if req.Device != nil {
		result["device"] = req.Device
	}
	// Not gated by requestedAttributes, same as device: it's a scoring/
	// compliance signal about how thoroughly this session was itself
	// verified, not raw personal data - a relying party should always be
	// able to see it, the way it's always able to see device metadata.
	result["assurance"] = computeAssurance(fd, req, verifiedChipChecks, sess.ReferencePhoto == "")
	return result
}

// faceReferenceRelyingParty is a result's faceReference when the face was
// matched against the relying party's own photo rather than the chip's DG2.
const faceReferenceRelyingParty = "relying_party"

// releasedDocumentImage is docImage as the result carries it: its BSN region
// blurred under a BlurBSN policy.
func releasedDocumentImage(sess session.Session, docImage documentImageInfo, redaction privacy.RedactionPolicy) *documentImageInfo {
	if redaction.BlurBSN && docImage.BSNRegion != nil {
		r := redact.Rect{X: docImage.BSNRegion.X, Y: docImage.BSNRegion.Y, W: docImage.BSNRegion.W, H: docImage.BSNRegion.H}
		if blurred, mime, err := redact.Region(docImage.ImageBase64, docImage.MimeType, r); err != nil {
			slog.Warn("identity proofing: could not blur the BSN region", slog.String("session_id", sess.ID), slog.Any("error", err))
		} else {
			docImage.ImageBase64, docImage.MimeType = blurred, mime
		}
	}
	return &docImage
}

// assuranceInfo is a session's computed assurance level - not to be
// confused with flow.FlowDefinition.RequiredAssuranceLevel (an eIDAS
// low/substantial/high requirement a flow author sets in advance). This is
// the opposite direction: what this specific session's own submitted
// evidence actually achieved, scored after the fact against the checks that
// applied to it, decomposed into their individual sub-checks (Categories)
// for accuracy - a category like nfc.passive_auth genuinely bundles several
// independent facts (SOD signature validity, per-data-group hash matching,
// CSCA trust-chain validity), and a session getting two of three right is
// meaningfully more assured than one getting none, which scoring the
// category as a single pass/fail unit would erase. Level/Score are always
// computed from the same item counts Categories breaks down, never
// independently. See flow.FlowDefinition.AssuranceTiers for how the
// Level cutoffs themselves are configured per flow/tenant.
type assuranceInfo struct {
	// Level is whichever tier in the effective AssuranceTiers ladder
	// (flow.FlowDefinition.EffectiveAssuranceTiers) Score clears - see
	// flow.LevelForScore.
	Level string `json:"level"`
	// Score is ChecksPassed/ChecksTotal, rounded to 3 decimals; 0 when
	// ChecksTotal is 0 (nothing to score against).
	Score        float64                 `json:"score"`
	ChecksPassed int                     `json:"checksPassed"`
	ChecksTotal  int                     `json:"checksTotal"`
	Categories   []assuranceCategoryInfo `json:"categories,omitempty"`
	// EIDASLevel is this session's *achieved* eIDAS level (requirements.md
	// §2's "assurance_level computed per session from the checks that
	// passed"), computed from which specific checks actually verified for
	// this session — a third, distinct thing from both Level above (a
	// free-form percentage-of-checks-passed tier, not eIDAS vocabulary —
	// see this struct's own package-level doc comment) and
	// flow.FlowDefinition.RequiredAssuranceLevel (declared in advance, never
	// computed from outcomes). See computeEIDASAssuranceLevel for the exact
	// rule and why it never reports "high" even when every check this
	// codebase computes passed.
	EIDASLevel flow.AssuranceLevel `json:"eidasLevel,omitempty"`
}

// assuranceCategoryInfo is one flow.Check's own item-level breakdown within
// assuranceInfo.Categories - see checkItems.
type assuranceCategoryInfo struct {
	Check flow.Check `json:"check"`
	// State is this check's overall checkState (see overallCheckState) -
	// "pass"/"fail" mean it actually ran and scored (reflected in
	// ItemsPassed/ItemsTotal below); "not_applicable" means the underlying
	// capability didn't exist for this submission (e.g. no Active
	// Authentication key on the chip, no anti-spoof model in the serving
	// engine) and "not_run" means it was selected but the step that
	// produces it never executed - neither counts toward
	// ChecksPassed/ChecksTotal, and neither is evidence the check failed.
	State       checkState `json:"state"`
	ItemsPassed int        `json:"itemsPassed"`
	ItemsTotal  int        `json:"itemsTotal"`
}

var defaultAssuranceChecks = []flow.Check{
	flow.CheckNFCPassiveAuth, flow.CheckNFCChipAuth, flow.CheckFaceMatch, flow.CheckFaceLiveness,
}

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

// checkState is one check's (or one sub-item's) outcome - see
// assuranceCategoryInfo.State and checkItems. Deliberately not a bool:
// "the underlying capability didn't apply to this submission" (e.g. a chip
// with no Active Authentication key, an engine with no anti-spoof model
// loaded) is not the same fact as "we checked, and it failed" - collapsing
// the two would either score a document down for a check it structurally
// could never pass, or silently drop it from the denominator based on
// inference rather than an explicit, auditable state.
type checkState string

const (
	// checkStatePass: the check ran and its criteria were met.
	checkStatePass checkState = "pass"
	// checkStateFail: the check ran, the capability existed, and its
	// criteria were not met.
	checkStateFail checkState = "fail"
	// checkStateNotApplicable: the capability this check depends on doesn't
	// exist for this submission (no AA/CA key on the chip, no anti-spoof
	// model wired into the serving engine, ...). Excluded from scoring.
	checkStateNotApplicable checkState = "not_applicable"
	// checkStateNotRun: the check was selected and the capability could
	// apply, but the step that would have produced evidence for it never
	// executed. Excluded from scoring.
	checkStateNotRun checkState = "not_run"
	// checkStateError: the check was attempted but verification itself
	// failed to complete (as opposed to completing and finding the
	// criteria unmet). Scored the same as checkStateFail - conservative,
	// since an unresolved verification is not evidence of a pass.
	checkStateError checkState = "error"
)

// boolCheckState maps a *bool evidence field (nil meaning "never computed")
// to a checkState, for the common case of a single pass/fail fact that
// either ran or didn't.
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

// checkItems decomposes one flow.Check into its individual sub-facts (e.g.
// nfc.passive_auth bundles SOD signature validity, per-data-group hash
// matching, and CSCA trust-chain validity) - see assuranceInfo's own doc
// comment for why that decomposition matters for scoring accuracy. Each
// item's State is computed independently: see checkState's doc comment for
// why "didn't apply" and "ran and failed" are kept distinct rather than
// both reading as a plain false.
func checkItems(fd *flow.FlowDefinition, check flow.Check, req appResultRequest, checks *chipChecksInfo) []assuranceItem {
	switch check {
	case flow.CheckNFCPassiveAuth:
		var pa *passiveAuthInfo
		if checks != nil {
			pa = checks.PassiveAuthentication
		}
		if pa == nil {
			// No mrtdEvidence was ever verified for this submission - the
			// nfc_read step never ran, not a failed verification.
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
			// Either the nfc_read step never ran, or it ran but the chip
			// carries no DG15/DG13 Active/Chip Authentication key at all
			// (see resolveAAKeyDataGroup) - either way, nothing to
			// authenticate, not evidence the document failed the check.
			return []assuranceItem{{State: checkStateNotApplicable}}
		}
		return []assuranceItem{{State: boolCheckState(aa.Passed)}}
	case flow.CheckFaceMatch:
		if req.Biometrics == nil || req.Biometrics.FaceVerified == nil {
			return []assuranceItem{{State: checkStateNotRun}}
		}
		passed := *req.Biometrics.FaceVerified
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
			// No real anti-spoof model ran for this submission - either the
			// serving engine has none loaded (api.checkLiveness fell back
			// to the frame-distinctness heuristic) or this path doesn't
			// wire liveness in at all yet (bound_login.go,
			// LivenessResult == "not_performed"). The capability wasn't
			// there for this submission, not a failed check - scoring a
			// heuristic guess as a real pass/fail would misrepresent
			// assurance.
			return []assuranceItem{{State: checkStateNotApplicable}}
		}
		if req.Biometrics.LivenessResult == "passed" {
			return []assuranceItem{{State: checkStatePass}}
		}
		return []assuranceItem{{State: checkStateFail}}
	default:
		// A check outside the small set this codebase actually computes
		// (mrz.parse, viz.ocr, document.tamper, ...) - unlike
		// checkStateNotApplicable/checkStateNotRun above, this is not a
		// capability gap for this particular submission, it's permanently
		// true for every submission, so it must never be silently excluded
		// from scoring: a flow author who lists one of these in
		// RequiredChecks documents intent without anything here computing
		// it, and scoring must not overstate assurance just because nothing
		// exists yet to fail it honestly.
		return []assuranceItem{{State: checkStateFail}}
	}
}

// overallCheckState reduces items (checkItems' output) to a single
// checkState for the whole check - used wherever a caller needs one
// yes/no-ish answer (e.g. computeEIDASAssuranceLevel) rather than the
// per-item breakdown computeAssurance scores. Priority order: any real
// failure (Fail/Error) wins over "didn't apply"/"didn't run", which in turn
// wins over Pass - so a check is only ever reported as having passed when
// every one of its items actually did.
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

// checkOutcome is checkItems followed by overallCheckState - the single-
// state answer for one flow.Check, used by callers (like
// computeEIDASAssuranceLevel) that only care whether a check fully passed,
// not its per-item breakdown.
func checkOutcome(fd *flow.FlowDefinition, check flow.Check, req appResultRequest, checks *chipChecksInfo) checkState {
	return overallCheckState(checkItems(fd, check, req, checks))
}

// computeAssurance scores req/checks against fd's selected checks
// (assuranceChecksFor) into a session's assuranceInfo. A check's items
// (checkItems) that come back checkStatePass/checkStateFail/checkStateError
// count toward ChecksPassed/ChecksTotal; checkStateNotApplicable/
// checkStateNotRun items are excluded entirely, per checkState's own doc
// comment - not inferred from a missing/zero value, but an explicit state
// checkItems itself returns, so a check that was never applicable to this
// submission (e.g. face.liveness with no anti-spoof model loaded, or
// nfc.chip_auth on a chip with no AA key) never silently drags the score
// down for a criterion it could not have met.
//
// chipReference is whether a face match ran against the chip's own DG2
// rather than a relying party's referencePhoto (see faceMatchReference).
func computeAssurance(fd *flow.FlowDefinition, req appResultRequest, checks *chipChecksInfo, chipReference bool) assuranceInfo {
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
				// excluded - see the doc comment above.
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
		EIDASLevel: computeEIDASAssuranceLevel(fd, req, checks, chipReference),
	}
}

// computeEIDASAssuranceLevel is the eIDAS level this session achieved: the
// highest of flow.LevelRequirements whose every check verified, its face, when
// the level names a provider, verified by that provider against the chip's own
// portrait (chipReference). "" when fd sets no RequiredAssuranceLevel (nothing
// to hold the session to, so no level is claimed) or when not even low was
// reached. A check that did not apply to this document (a chip without an
// Active Authentication key) has not verified, so it does not count: the level
// reports what was proven, never what could not be tested.
func computeEIDASAssuranceLevel(fd *flow.FlowDefinition, req appResultRequest, checks *chipChecksInfo, chipReference bool) flow.AssuranceLevel {
	if fd == nil || fd.RequiredAssuranceLevel == "" {
		return ""
	}
	achieved := flow.AssuranceLevel("")
	for _, level := range flow.LevelRequirements {
		if !meetsLevelRequirement(fd, level, req, checks, chipReference) {
			break
		}
		achieved = level.Level
	}
	return achieved
}

// meetsLevelRequirement reports whether this session verified everything
// level requires.
func meetsLevelRequirement(fd *flow.FlowDefinition, level flow.LevelRequirement, req appResultRequest, checks *chipChecksInfo, chipReference bool) bool {
	for _, c := range level.Checks {
		if checkOutcome(fd, c, req, checks) != checkStatePass {
			return false
		}
	}
	if level.FaceProvider == "" {
		return true
	}
	return chipReference && req.Biometrics != nil && req.Biometrics.Engine == string(level.FaceProvider)
}

// dutchIssuingState is the ICAO 9303 3-letter country code the Netherlands
// issues its passports/ID cards under — the only issuing state whose DG11
// "personal number" field is a BSN (see applyBSNPolicy).
const dutchIssuingState = "NLD"

// applyBSNPolicy enforces the tenant's BSN policy on doc.PersonalNumber in
// place, before doc is ever assigned into the stored/returned result
// (requirements.md §3: "BSN handling per configuration: retrieve, do not
// retrieve, or mask" and "BSN and photo redaction happen server side before
// any result leaves the service" — i.e. before storage, not just before
// returning). Applies regardless of sess.RequestedAttributes: a relying
// party asking for dg11 does not override the tenant's own privacy
// configuration — attrDG11 filtering in buildResult still runs on top of
// this and can only narrow further, never widen back to the raw value.
//
// Only touches documents issued by the Netherlands: DG11's personal number
// field carries other countries' national identifiers too, and this policy
// is specifically about the Dutch BSN, not every country's equivalent
// field.
func applyBSNPolicy(doc *documentInfo, policy privacy.BSNPolicy) {
	if doc.PersonalNumber == "" || doc.IssuingState != dutchIssuingState {
		return
	}
	switch policy.Effective() {
	case privacy.BSNPolicyOmit:
		doc.PersonalNumber = ""
	case privacy.BSNPolicyMask:
		doc.PersonalNumber = bsn.Mask(doc.PersonalNumber)
	case privacy.BSNPolicyRetrieve:
		// keep as extracted
	}
}

// redactBSNFromEvidence drops ev's raw "DG11" entry when policy means the
// BSN must not be retrievable, mirroring applyBSNPolicy's gating (Dutch-
// issued documents only) but for the raw chip bytes rather than the parsed
// field. DG11's raw bytes carry the same BSN applyBSNPolicy just masked or
// omitted from doc — unlike that string field there's no meaningful masked
// encoding of a raw ASN.1 data group, so both BSNPolicyMask and
// BSNPolicyOmit drop the entry outright rather than trying to redact within
// it, before it's ever persisted into Session.Steps.NFC.Raw
// (handleSubmitNFCStep marshals ev's whole struct, DataGroups included, and
// that call site's own doc comment already establishes Steps is "stored
// independently and never re-redacted afterward").
//
// Dropping DG11 doesn't affect Passive/Active Authentication verified now or
// re-verified later from stored evidence (finishSession):
// mrtdverify.VerifyPassive/VerifyPassiveICAO only ever require DG1/DG2, and
// Active Authentication's key comes from DG15 (or DG13 for an EU driving
// licence) — DG11 is never checked or referenced by either.
//
// doc.PersonalNumber isn't checked here (unlike applyBSNPolicy): the chip
// can carry a BSN in DG11 even when the MRZ/VIZ personal-number field is
// empty (older Dutch documents), so an empty parsed field must not skip
// redacting the raw evidence that might still carry one.
func redactBSNFromEvidence(ev *mrtdEvidenceRequest, doc *documentInfo, policy privacy.BSNPolicy) {
	if ev == nil || doc == nil || doc.IssuingState != dutchIssuingState {
		return
	}
	switch policy.Effective() {
	case privacy.BSNPolicyMask, privacy.BSNPolicyOmit:
		delete(ev.DataGroups, "DG11")
	}
}

// verifyMrtdEvidence runs Passive (and, where the chip supports it, Active)
// Authentication against ev's raw chip bytes and returns the server-computed
// chipChecksInfo — the only source buildResult ever uses for chipChecks. A
// nil ev (no evidence submitted, e.g. an older client) returns (nil, nil):
// no chipChecks claim at all, rather than trusting one the app made up.
//
// Passive Authentication runs one of two ways depending on ev.DocumentType
// (see documentTypeEUDrivingLicence): mrtdverify.VerifyPassiveICAO
// for passports/ID cards (adds the SOD-country-vs-DG1-country cross-check),
// mrtdverify.VerifyPassive for EU driving licences (can't use the typed
// path at all — see mrtdverify's package doc comment).
//
// expectedAAChallenge is the hex-encoded challenge this server issued for
// the session (session.Session.AAChallenge, handed to the app via
// appSessionView) — Active Authentication evidence is only trusted as proof
// of live chip possession if its nonce matches this exactly. See
// aaChallengeMatches for why: gmrtd's own activeauth.VerifyEvidence doc
// comment calls out that it "does not enforce that the nonce matches the
// challenge used in the original session" and that callers must do so
// themselves for relay-attack prevention (or use verifier.Verifier.WithAAChallenge,
// which does the same byte comparison at that package's layer).
//
// A non-nil error means the evidence itself was malformed (bad hex, unknown
// data group, a key referenced by AAKeyDataGroup that isn't in DataGroups,
// or — for the ICAO path — a missing mandatory DG1/DG2) — the caller should
// treat that as a bad request. A verification that ran and failed (bad
// signature, untrusted or wrong-country chain, tampered data group, or a
// nonce that doesn't match expectedAAChallenge) is reflected in the returned
// chipChecksInfo's fields, not an error.
func verifyMrtdEvidence(ev *mrtdEvidenceRequest, expectedAAChallenge string) (*chipChecksInfo, error) {
	if ev == nil {
		return nil, nil
	}

	aaKeyDataGroup := resolveAAKeyDataGroup(ev)
	pool, err := mrtdverify.CertPoolFor(aaKeyDataGroup)
	if err != nil {
		return nil, fmt.Errorf("loading CSCA trust anchor: %w", err)
	}

	verifyPassive := mrtdverify.VerifyPassiveICAO
	if ev.DocumentType == documentTypeEUDrivingLicence {
		verifyPassive = mrtdverify.VerifyPassive
	}
	passive, err := verifyPassive(ev.EFSOD, ev.DataGroups, pool)
	if err != nil {
		return nil, fmt.Errorf("verifying passive authentication: %w", err)
	}
	checks := &chipChecksInfo{
		PassiveAuthentication: &passiveAuthInfo{
			SODSignatureValid:    &passive.SODSignatureValid,
			DataGroupHashesValid: &passive.DataGroupHashesValid,
			InvalidDataGroups:    passive.InvalidDataGroups,
			CSCATrustChainValid:  &passive.CSCATrustChainValid,
			IssuingCSCA:          passive.IssuingCSCA,
			DocumentComplete:     &passive.DocumentComplete,
			DocumentVerifyErr:    passive.DocumentVerifyErr,
		},
	}
	// TamperDetected is this server's own verdict, not the app's: Passive
	// Authentication as a whole (signature + trust chain + every submitted
	// data group's hash, PLUS gmrtd's own document-completeness check —
	// DocumentComplete, see mrtdverify.PassiveResult) must hold for the
	// chip's data to be trusted at all. See authenticityFailure, which uses
	// this to override the app's claimed status outright.
	tamperDetected := !passive.SODSignatureValid || !passive.CSCATrustChainValid || !passive.DataGroupHashesValid || !passive.DocumentComplete
	checks.TamperDetected = &tamperDetected

	if aaKeyDataGroup != "" {
		keyHex, ok := ev.DataGroups[aaKeyDataGroup]
		if !ok {
			return nil, fmt.Errorf("aaKeyDataGroup %q not present in dataGroups", aaKeyDataGroup)
		}
		active, err := mrtdverify.VerifyActive(keyHex, ev.Nonce, ev.AASignature)
		if err != nil {
			return nil, fmt.Errorf("verifying active authentication: %w", err)
		}
		if active.Attempted && !aaChallengeMatches(ev.Nonce, expectedAAChallenge) {
			// The signature may genuinely verify — it was computed over
			// whatever nonce ev.Nonce actually contains — but that nonce
			// wasn't the one this server issued for this session. Trusting
			// it anyway would accept a captured genuine response replayed
			// from elsewhere (or simply relayed live to a different chip) as
			// proof this chip is present now, which defeats the point of
			// Active Authentication. Force the outcome to failed regardless
			// of what the raw signature check found.
			active.Passed = false
		}
		method := "chip_authentication"
		if aaKeyDataGroup == "DG15" {
			method = "active_authentication"
		}
		checks.ActiveAuthentication = &activeAuthInfo{
			Attempted: &active.Attempted,
			Passed:    &active.Passed,
			Method:    method,
		}
		// CloneDetected: AA was attempted but the signature didn't verify
		// (copied data, wrong/missing private key), including a nonce mismatch.
		cloneDetected := active.Attempted && !active.Passed
		checks.CloneDetected = &cloneDetected
	}

	return checks, nil
}

// aaChallengeMatches reports whether nonceHex is exactly the challenge this
// server issued for the session (expectedHex) — see verifyMrtdEvidence's
// expectedAAChallenge doc comment. Constant-time, matching
// authenticateAPIKey's convention for comparing a value an attacker might be
// probing. Either side being empty or not valid hex is never a match:
// expectedHex is empty only if the session predates AAChallenge existing at
// all, which fails closed rather than skipping the check.
func aaChallengeMatches(nonceHex, expectedHex string) bool {
	if nonceHex == "" || expectedHex == "" {
		return false
	}
	nonce, err := hex.DecodeString(nonceHex)
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(expectedHex)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(nonce, expected) == 1
}

// authenticityFailure reports whether checks — this server's own Passive/
// Active Authentication verdict, never the app's self-reported claim — found
// the chip's data untrustworthy, and if so which error code explains why.
// handleAppSessionResult uses this to override the app's claimed status
// outright: a submission whose evidence didn't authenticate can never end up
// approved or sent to manual review, regardless of what the app asserts.
// checks == nil (no mrtdEvidence submitted at all, e.g. an older client or a
// non-NFC method) is not a failure — there's nothing to enforce against.
func authenticityFailure(checks *chipChecksInfo) (failed bool, errorCode string) {
	if checks == nil {
		return false, ""
	}
	if checks.TamperDetected != nil && *checks.TamperDetected {
		return true, "DOC_TAMPERED"
	}
	if checks.CloneDetected != nil && *checks.CloneDetected {
		return true, "CHIP_CLONE_DETECTED"
	}
	return false, ""
}

// ---- relying-party-facing handlers ----------------------------------------

// sessionResultView is the relying party's outcome-only view of a session —
// a thin projection of fields already sitting on session.Session.Result,
// which is fully data-minimised at write time by buildResult (see
// handleAppSessionResult), so there's no extra filtering to do here.
type sessionResultView struct {
	ID          string                    `json:"id"`
	Status      session.Status            `json:"status"`
	ErrorCode   string                    `json:"errorCode,omitempty"`
	Result      map[string]any            `json:"result,omitempty"`
	CompletedAt *time.Time                `json:"completedAt,omitempty"`
	Devices     []deviceParticipationView `json:"devices,omitempty"`
}

// sessionStatusView is a session's outcome without personal data: status,
// the assurance summary and which devices took part. It is what a relying
// party reconciles on, so reading it is not audited as a personal-data read.
type sessionStatusView struct {
	ID          string         `json:"id"`
	Status      session.Status `json:"status"`
	ErrorCode   string         `json:"errorCode,omitempty"`
	CompletedAt *time.Time     `json:"completedAt,omitempty"`
	FlowVersion int            `json:"flowVersion,omitempty"`
	Assurance   any            `json:"assurance,omitempty"`
	// Disclosure is whether a Yivi disclosure is part of the result.
	Disclosure bool                      `json:"disclosure,omitempty"`
	Devices    []deviceParticipationView `json:"devices,omitempty"`
}

// ---- app-facing handlers ---------------------------------------------------

// handleAppSession is what the app calls right after opening the deep
// link/QR: what to collect, who's asking, how long it has. Fetching it also
// marks the session opened — there's no separate "I opened this" call, the
// app asking what to do *is* opening it. Idempotent: only a fresh (created)
// session is transitioned; a re-poll at opened/in_progress or any later
// (terminal) status just returns the current view instead of retrying a
// transition that's already happened or is no longer valid.
//
//	@Summary	Get proofing instructions for the app
//	@Tags		proofing-app
//	@Produce	json
//	@Param		token	path		string	true	"Session token"
//	@Success	200		{object}	api.appSessionView
//	@Failure	404		{object}	map[string]string
//	@Failure	409		{object}	map[string]string
//	@Router		/api/v1/app/{token} [get]
func (s *Server) handleAppSession(w http.ResponseWriter, r *http.Request) {
	sess, caller, ok := s.appSessionByPathToken(w, r)
	if !ok {
		return
	}
	if sess.Status == session.StatusCreated {
		opened := false
		updated, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
			var err error
			opened, err = s.openLocked(sess, time.Now().UTC())
			return err
		})
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		sess = updated
		if opened {
			s.auditProofing(sess, eventSessionOpened, nil)
		}
	}
	view, err := s.buildAppSessionView(r, sess, caller.role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not resolve flow: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// buildAppSessionView is the app-facing view of sess, as seen by the
// device controlling role's slot ("" before anyone claimed the session).
func (s *Server) buildAppSessionView(r *http.Request, sess session.Session, role session.DeviceRole) (appSessionView, error) {
	resolvedFlow, err := s.resolveSessionFlow(r.Context(), sess)
	if err != nil {
		return appSessionView{}, err
	}
	var steps []flow.Step
	if resolvedFlow != nil {
		steps = resolvedFlow.Steps
	}
	selfieLocation := flow.LocationBrowser
	if resolvedFlow != nil {
		selfieLocation = resolvedFlow.EffectiveSelfieLocation()
	}
	var nativeHandoff *nativeHandoffInfo
	if nativeHandoffPending(sess, resolvedFlow) {
		nativeHandoff = &nativeHandoffInfo{Role: session.DeviceRoleNative, Claimed: sess.Access.Native != nil}
	}
	current := currentStep(sess, resolvedFlow)
	var chipAccess *session.ChipAccessKey
	if current != nil && *current == string(flow.StepNFCRead) && sess.Steps.Document != nil {
		chipAccess = sess.Steps.Document.ChipAccess
	}
	var faceReference *photoInfo
	if current != nil && isFaceStep(flow.Step(*current)) && selfieLocation == flow.LocationNative {
		if image, mime, ok := s.faceMatchReference(sess); ok {
			faceReference = &photoInfo{ImageBase64: image, MimeType: mime}
		}
	}
	var faceVerification *faceVerificationInfo
	var faceProvider flow.FaceProvider
	if resolvedFlow != nil && selfieLocation == flow.LocationNative && slices.ContainsFunc(steps, isFaceStep) {
		faceProvider = s.faceProviderFor(resolvedFlow)
	}
	if s.cfg.Regula != nil && faceProvider == flow.FaceProviderRegula {
		faceVerification = &faceVerificationInfo{Provider: faceProviderRegula, FaceAPIURL: s.cfg.RegulaFaceAPIPublicURL, Tag: regulaTag(sess)}
		s.queueRegulaSweep(r.Context(), sess)
	}
	device := callerDeviceView{Authorized: role != "" || sess.Method == session.MethodBiometricBoundLogin, Role: role}
	if role != "" {
		device.deviceView = s.toDeviceView(sess.Access.Slot(role))
	}
	return appSessionView{
		ID: sess.ID, Method: sess.Method, RelyingParty: s.tenantDisplayName(r.Context(), sess.TenantID), Status: sess.Status, Language: requestLanguage(r, sess.Language),
		// RequestedAttributes controls which result data the tenant receives;
		// SelfieLocation controls which client performs the single face-
		// verification stage. Keep these concerns independent.
		RequestedAttributes: sess.RequestedAttributes, Steps: steps, ExpiresAt: sess.ExpiresAt,
		SelfieLocation: selfieLocation,
		AAChallenge:    sess.AAChallenge, RequiredChecks: assuranceChecksFor(resolvedFlow),
		CompletedSteps: sess.CompletedSteps(), NativeHandoff: nativeHandoff,
		ResetCount: sess.ResetCount, ChangeKey: s.appSessionChangeKey(sess, time.Now()),
		FlowID: sess.Flow, FlowVersion: sess.FlowVersion,
		CurrentStep: current, ChipAccess: chipAccess, FaceReference: faceReference, FaceVerification: faceVerification, FaceProvider: faceProvider,
		StepResults: stepResults(sess, resolvedFlow), Lifecycle: sessionLifecycle(sess), ReadyToSubmit: readyToSubmit(sess, resolvedFlow), Device: device,
		Devices: map[string]deviceView{
			string(session.DeviceRoleWeb):    s.toDeviceView(sess.Access.Web),
			string(session.DeviceRoleNative): s.toDeviceView(sess.Access.Native),
		},
	}, nil
}

// handleAppSessionEvents long-polls until the app-facing session view changes
// in status or completed steps. This lets browser clients notice an NFC step
// without repeatedly polling from JavaScript.
func (s *Server) handleAppSessionEvents(w http.ResponseWriter, r *http.Request) {
	sess, caller, ok := s.appSessionByPathToken(w, r)
	if !ok {
		return
	}
	baseline := r.URL.Query().Get("since")
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.SessionEventsWait)
	defer cancel()
	ticker := time.NewTicker(s.cfg.SessionEventsPoll)
	defer ticker.Stop()

	for s.appSessionChangeKey(sess, time.Now()) == baseline {
		select {
		case <-ctx.Done():
			if r.Context().Err() != nil {
				// The device hung up mid-wait - a killed app does exactly that.
				if d := sess.Access.Slot(caller.role); d != nil {
					s.markDisconnected(sess, caller.role, d.ID)
				}
				return
			}
			s.handleAppSession(w, r)
			return
		case <-ticker.C:
			updated, err := s.sessions.Get(sess.TenantID, sess.ID)
			if err != nil {
				writeError(w, http.StatusNotFound, "session not found")
				return
			}
			sess = updated
		}
	}
	s.handleAppSession(w, r)
}

// appSessionChangeKey is what handleAppSessionEvents waits to change.
// ResetCount is part of it because a reset from opened changes neither
// status nor completed steps, yet every client still has to notice it;
// Access.Generation and the slots' states so a handover (the old device
// must stop) or a device going inactive (the other may offer a handover)
// wakes every waiting client too.
func (s *Server) appSessionChangeKey(sess session.Session, now time.Time) string {
	states := ""
	for _, d := range []*session.DeviceAccess{sess.Access.Web, sess.Access.Native} {
		if d != nil {
			states += d.State
			if s.deviceStale(d, now) {
				states += ":stale"
			}
		}
		states += ","
	}
	return string(sess.Status) + "|" + strings.Join(sess.CompletedSteps(), ",") + "|" + strconv.Itoa(sess.ResetCount) +
		"|" + strconv.Itoa(sess.Access.Generation) + "|" + states
}

// nativeHandoffPending reports whether the browser flow should currently
// offer the vcmrtd deep link/QR. Two cases:
//
//   - No resolved flow at all (no flow.Store configured, or no flow
//     definition created yet — fd is nil): there's no step model to drive
//     the browser flow's screens with, so this nfc_passport session can
//     only ever finish through the older POST .../result (vcmrtd's own
//     full flow: it scans the MRZ itself and reads the chip) — the handoff
//     is offered unconditionally, the same way it always has been for this
//     case. Never offered for biometric_bound_login, which has no vcmrtd
//     concept at all.
//   - A resolved flow: offered once nfc_read (which, per flow.Validate,
//     always means document_capture too — both are fulfilled by the same
//     vcmrtd submission) hasn't landed yet, or the selfie cluster is
//     configured native and hasn't either.
func nativeHandoffPending(sess session.Session, fd *flow.FlowDefinition) bool {
	if fd == nil {
		return sess.Method == session.MethodNFCPassport
	}
	steps := fd.Steps
	selfieLocation := fd.EffectiveSelfieLocation()
	hasNFC := slices.Contains(steps, flow.StepNFCRead)
	hasDocumentPhoto := slices.Contains(steps, flow.StepDocumentPhoto)
	hasSelfieCluster := slices.Contains(steps, flow.StepFaceVerification) || slices.Contains(steps, flow.StepSelfie) || slices.Contains(steps, flow.StepLiveness) || slices.Contains(steps, flow.StepFaceMatch)
	selfieNative := hasSelfieCluster && selfieLocation == flow.LocationNative

	return (hasNFC && sess.Steps.NFC == nil) || (hasDocumentPhoto && sess.Steps.DocumentPhoto == nil) ||
		(selfieNative && sess.Steps.Selfie == nil)
}

// sessionByPathToken resolves the session for the app-facing routes, where
// the token is the only credential — the app never learns the session id or
// tenant on its own.
func (s *Server) sessionByPathToken(w http.ResponseWriter, r *http.Request) (session.Session, bool) {
	sess, err := s.sessions.Authenticate(r.PathValue("token"))
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return session.Session{}, false
	}
	return sess, true
}

// handleAppSessionResult is IPS's single-shot result for a session without a
// flow. Every wallet session runs a flow, so it only ever refuses: the app
// sends its evidence step by step instead.
func (s *Server) handleAppSessionResult(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.appSessionByPathToken(w, r); !ok {
		return
	}
	writeErrorCode(w, http.StatusConflict, errCodeFlowStepsRequired,
		"this session runs a flow: submit each step through POST .../steps/... and then POST .../submit")
}
