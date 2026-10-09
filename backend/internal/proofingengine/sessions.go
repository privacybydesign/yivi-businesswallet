// The app's view of a session and its long-poll, result building (data
// minimisation, BSN policy, redaction), chip verification and assurance
// scoring. The relying-party side is rp.go; device access is device_access.go.
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

// Session events. Each is logged; the ones the wallet reconciles on also
// notify it (notifyEventTypes).
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

// eventTypeForStatus is the event for reaching status with a submitted result.
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

// auditStepSubmitted logs a step's evidence landing, with where the session
// now stands in fd's step order.
func (s *Server) auditStepSubmitted(sess session.Session, fd *flow.FlowDefinition, stage string, extra map[string]any) {
	s.auditProofing(sess, eventResultSubmitted, stepAuditDetails(sess, fd, stage, "submitted", extra))
}

// auditStepStarted logs the subject beginning a step: one in_progress event
// per step.
func (s *Server) auditStepStarted(sess session.Session, fd *flow.FlowDefinition, stage string, extra map[string]any) {
	inProgress := sess
	inProgress.Status = session.StatusInProgress
	s.auditProofing(inProgress, eventSessionInProgress, stepAuditDetails(sess, fd, stage, "started", extra))
}

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

// effectivePrivacyPolicy is the flow's BSN and redaction policy, else the
// default (BSN kept, nothing blurred).
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

// resolveSessionFlow is the flow version sess was pinned to at creation, never
// the currently active one, so editing a flow does not change a running
// session. (nil, nil) when no flow governs the session.
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

// requestedAttributesOf is what fd releases: its own list, or everything its
// steps collect when it lists none. Only attrOutcomeOnly, chosen explicitly,
// releases nothing but the outcome.
func requestedAttributesOf(fd flow.FlowDefinition) []string {
	if len(fd.RequestedAttributes) > 0 {
		return fd.RequestedAttributes
	}
	if attrs := attributesForSteps(fd.Steps); len(attrs) > 0 {
		return attrs
	}
	return []string{attrOutcomeOnly}
}

// attributesForSteps is every result attribute steps collect.
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

// Rejection codes of a face step that did not verify the person
// (faceStepFailure), an expired document (flowComplianceFailure) and an
// achieved eIDAS level below the flow's required one (sessionOutcome).
const (
	errCodeDocExpired      = "DOC_EXPIRED"
	errCodeDocTypeRefused  = "DOCUMENT_TYPE_NOT_ACCEPTED"
	errCodeCountryRefused  = "DOCUMENT_COUNTRY_NOT_ACCEPTED"
	errCodeLivenessFailed  = "LIVENESS_FAILED"
	errCodeAssuranceNotMet = "ASSURANCE_NOT_MET"
)

// hasFaceStep reports whether fd has any of the face steps the one selfie
// submission fulfils.
func hasFaceStep(fd *flow.FlowDefinition) bool {
	return slices.ContainsFunc(fd.Steps, isFaceStep)
}

// faceStepFailure reports a face step that did not verify the person:
//   - in a step that compares faces, no passing match, at any level;
//   - a capture that was not live, only when fd requires a level or lists
//     face.liveness. Otherwise the match alone decides.
//
// Regula matches only a live capture, so a failed liveness leaves no match at
// all: a face step that compares faces still fails, with FACE_NO_MATCH.
func faceStepFailure(fd *flow.FlowDefinition, req appResultRequest, checks *chipChecksInfo) (failed bool, errorCode string) {
	if fd == nil || !hasFaceStep(fd) {
		return false, ""
	}
	livenessGates := fd.RequiredAssuranceLevel != "" || flowListsCheck(fd, flow.CheckFaceLiveness)
	if livenessGates && checkOutcome(fd, flow.CheckFaceLiveness, req, checks) == checkStateFail {
		return true, errCodeLivenessFailed
	}
	if flowNeedsFaceMatch(fd) && checkOutcome(fd, flow.CheckFaceMatch, req, checks) != checkStatePass {
		return true, errCodeFaceNoMatch
	}
	return false, ""
}

// day is a calendar day in UTC.
const day = 24 * time.Hour

// flowComplianceFailure reports a submission outside what fd accepts: the
// document type, issuing country or expiry, or a face step without the
// server's own face evidence. Whether that evidence verified the person is
// faceStepFailure's.
func flowComplianceFailure(fd *flow.FlowDefinition, sess session.Session, req appResultRequest, now time.Time) (failed bool, errorCode string) {
	if fd == nil {
		return false, ""
	}
	if req.Document != nil {
		if len(fd.AcceptedDocumentTypes) > 0 && !slices.Contains(fd.AcceptedDocumentTypes, req.Document.Type) {
			return true, errCodeDocTypeRefused
		}
		if len(fd.AcceptedIssuingCountries) > 0 && !slices.Contains(fd.AcceptedIssuingCountries, flow.IssuingStateCode(req.Document.IssuingState)) {
			return true, errCodeCountryRefused
		}
		if req.Document.DateOfExpiry != "" && documentExpired(req.Document.DateOfExpiry, now) {
			return true, errCodeDocExpired
		}
	}
	if hasFaceStep(fd) && sess.Steps.Selfie == nil {
		return true, "FACE_STEP_NOT_COMPLETED"
	}
	return false, ""
}

// ---- payloads -------------------------------------------------------------

// appSessionView is what the app-facing GET returns: what the app needs to run
// the flow, and the org by display name only.
type appSessionView struct {
	ID           string         `json:"id"`
	Method       session.Method `json:"method"`
	RelyingParty string         `json:"relyingParty"`
	// Status lets the browser tell a finished session from a running one.
	Status session.Status `json:"status"`
	// Language is "en" or "nl": the session's language when shipped, else the
	// caller's Accept-Language, else "en".
	Language            string   `json:"language,omitempty"`
	RequestedAttributes []string `json:"requestedAttributes,omitempty"`
	// Steps is the flow's step order, which tells the app what to do; empty without
	// a flow.
	Steps []flow.Step `json:"steps,omitempty"`
	// SelfieLocation says who runs the face step, the browser or the Idem app
	// ("browser" without a flow). document_capture and nfc_read are always the
	// app's.
	SelfieLocation flow.StepLocation `json:"selfieLocation"`
	ExpiresAt      time.Time         `json:"expiresAt"`
	// AAChallenge is the RND.IFD the app sends to the chip's INTERNAL AUTHENTICATE.
	// Evidence with another nonce is never proof of possession.
	AAChallenge string `json:"aaChallenge,omitempty"`
	// RequiredChecks are the checks the session is scored on; the app runs the ones
	// it performs (Active/Chip Authentication for nfc.chip_auth) because the flow
	// asks for them.
	RequiredChecks []flow.Check `json:"requiredChecks"`
	// CompletedSteps are the steps with evidence, so a reloaded or handed-over
	// client resumes at the right screen.
	CompletedSteps []string `json:"completedSteps,omitempty"`
	// NativeHandoff says the Idem app still has work: the chip read, and the face
	// step when it runs native. The browser mints the app's QR itself (POST
	// .../handover {role: "native"}).
	NativeHandoff *nativeHandoffInfo `json:"nativeHandoff,omitempty"`
	// ResetCount goes up with every reset (POST .../reset): a client that sees it
	// change drops what it collected and starts over.
	ResetCount int `json:"resetCount"`
	// ChangeKey is the ?since= for GET .../events; clients echo it back, so its
	// format may change.
	ChangeKey string `json:"changeKey"`

	// FlowID/FlowVersion name the flow definition governing the session.
	FlowID      string `json:"flowId,omitempty"`
	FlowVersion int    `json:"flowVersion,omitempty"`
	// CurrentStep is the step to continue with, "" once complete; clients resume
	// from it. Absent without a flow, where "" would wrongly read as complete.
	CurrentStep *string `json:"currentStep,omitempty"`
	// ChipAccess is the MRZ-derived chip access key, present only while the chip
	// read is the current step (chipAccessFor).
	ChipAccess *session.ChipAccessKey `json:"chipAccess,omitempty"`
	// FaceReference is the photo the face step is matched against (the chip
	// portrait, or the customer's photo on a flow without nfc_read), present while
	// the face step is current and runs in the Idem app.
	FaceReference *photoInfo `json:"faceReference,omitempty"`
	// FaceVerification tells the Idem app to run the face step as a Regula
	// liveness session, when the flow uses Regula and it is configured.
	FaceVerification *faceVerificationInfo `json:"faceVerification,omitempty"`
	// FaceProvider is the engine the Idem app runs the face step with.
	FaceProvider flow.FaceProvider         `json:"faceProvider,omitempty"`
	StepResults  map[string]stepResultView `json:"stepResults"`
	// Lifecycle is ACTIVE, COMPLETE (every step has a result and Status is
	// decided), EXPIRED or CANCELLED.
	Lifecycle string `json:"lifecycle"`
	// ReadyToSubmit: every step has a result and the session waits for POST
	// .../submit.
	ReadyToSubmit bool `json:"readyToSubmit,omitempty"`
	// Device is the caller's own authorization; Devices every slot's state, so one
	// client sees the other go inactive.
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

// appResultRequest is a session's evidence as finishSession assembles it from
// the steps. buildResult keeps only what the session requested.
type appResultRequest struct {
	Status    session.Status `json:"status"`
	ErrorCode string         `json:"errorCode,omitempty"`
	Document  *documentInfo  `json:"document,omitempty"`
	Photo     *photoInfo     `json:"photo,omitempty"`
	// Selfie is the live face the face step captured.
	Selfie *photoInfo `json:"selfie,omitempty"`
	// DocumentImage is the photographed front, DocumentImageBack the back.
	DocumentImage     *documentImageInfo   `json:"documentImage,omitempty"`
	DocumentImageBack *documentImageInfo   `json:"documentImageBack,omitempty"`
	ChipChecks        *chipChecksInfo      `json:"chipChecks,omitempty"`
	MrtdEvidence      *mrtdEvidenceRequest `json:"mrtdEvidence,omitempty"`
	Biometrics        *biometricsInfo      `json:"biometrics,omitempty"`
	Device            *deviceInfo          `json:"device,omitempty"`
}

// documentInfo is the identity read off the document's MRZ/DG1, plus the DG11
// extras. The shape follows vcmrtd's PassportMRZ/PassportData.
type documentInfo struct {
	Type         string `json:"type,omitempty"` // ICAO document code, e.g. "P" for passport
	Number       string `json:"number,omitempty"`
	IssuingState string `json:"issuingState,omitempty"`
	Nationality  string `json:"nationality,omitempty"`
	FirstName    string `json:"firstName,omitempty"`
	LastName     string `json:"lastName,omitempty"`
	// DisplayName prefers DG11's UTF-8 name over the transliterated MRZ name, as
	// vcmrtd does.
	DisplayName string `json:"displayName,omitempty"`
	Sex         string `json:"sex,omitempty"`

	DateOfBirth  string `json:"dateOfBirth,omitempty"`  // YYYY-MM-DD
	DateOfExpiry string `json:"dateOfExpiry,omitempty"` // YYYY-MM-DD

	// DG11 extras, when the chip has DG11 and dg11 was requested.
	PersonalNumber string `json:"personalNumber,omitempty"`
	PlaceOfBirth   string `json:"placeOfBirth,omitempty"`

	Validity *documentValidityInfo `json:"validity,omitempty"`
}

// documentValidityInfo is the MRZ check digits and the printed expiry, which
// say nothing about authenticity (that is chipChecksInfo).
type documentValidityInfo struct {
	DocumentNumberCheckDigitValid *bool `json:"documentNumberCheckDigitValid,omitempty"`
	DateOfBirthCheckDigitValid    *bool `json:"dateOfBirthCheckDigitValid,omitempty"`
	DateOfExpiryCheckDigitValid   *bool `json:"dateOfExpiryCheckDigitValid,omitempty"`
	CompositeCheckDigitValid      *bool `json:"compositeCheckDigitValid,omitempty"`
	NotExpired                    *bool `json:"notExpired,omitempty"`
}

// photoInfo is an image in a result, such as the chip's DG2 portrait. It is
// released only when requested (attrRequested).
type photoInfo struct {
	ImageBase64 string `json:"imageBase64,omitempty"`
	MimeType    string `json:"mimeType,omitempty"` // e.g. "image/jpeg", "image/jp2"
}

// imageRegion is a box in an image, normalised to [0,1] from the top left: where
// the BSN is printed, for internal/redact to cover.
type imageRegion struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// documentImageInfo is a photograph of the document's data page, as opposed to
// the chip portrait. Dutch ID cards print the BSN on it: BSNRegion, when known,
// is where buildResult covers it under a BlurBSN policy. Without it nothing is
// covered; the server never guesses where the BSN is.
type documentImageInfo struct {
	ImageBase64 string       `json:"imageBase64,omitempty"`
	MimeType    string       `json:"mimeType,omitempty"`
	BSNRegion   *imageRegion `json:"bsnRegion,omitempty"`
}

// mrtdEvidenceRequest is the raw chip data the server verifies itself: Passive
// Authentication (EF.SOD against a CSCA anchor, data-group hashes) and
// Active/Chip Authentication (the chip's signature over the server's
// challenge). Hex fields are hex-encoded.
type mrtdEvidenceRequest struct {
	EFSOD string `json:"efSod"`
	// DataGroups are the raw data groups as read: Passive Authentication hashes
	// these bytes.
	DataGroups map[string]string `json:"dataGroups"`
	// DocumentType picks the Passive Authentication path; empty is the ICAO path.
	// An explicit field because a passport without DG15 also has no
	// AAKeyDataGroup.
	DocumentType string `json:"documentType,omitempty"`
	// AAKeyDataGroup names the data group with the Active Authentication key:
	// DG15 for passports and ID cards, DG13 for driving licences; see
	// resolveAAKeyDataGroup for its default.
	AAKeyDataGroup string `json:"aaKeyDataGroup,omitempty"`
	// Nonce and AASignature are the challenge sent to the chip and its signature,
	// required with AAKeyDataGroup.
	Nonce       string `json:"nonce,omitempty"`
	AASignature string `json:"aaSignature,omitempty"`
}

// Document types of mrtdEvidenceRequest. Empty or "icao" is a passport or ID
// card, verified with mrtdverify.VerifyPassiveICAO, which also checks the
// signer's country against DG1's and narrows the trust pool to it.
const (
	// documentTypeEUDrivingLicence runs the generic mrtdverify.VerifyPassive:
	// licences use DG1/DG6/DG13 encodings the typed parsers cannot read, so there
	// is no country cross-check.
	documentTypeEUDrivingLicence = flow.DocumentTypeEUDrivingLicence
)

// resolveAAKeyDataGroup defaults ev.AAKeyDataGroup to DG15 when the client sent
// a DG15 but named no key group, as vcmrtd does; without it a genuine Active
// Authentication would never be verified. An explicit value wins. Driving
// licences (DG13) get no default.
func resolveAAKeyDataGroup(ev *mrtdEvidenceRequest) string {
	if ev.AAKeyDataGroup != "" {
		return ev.AAKeyDataGroup
	}
	if ev.DocumentType == documentTypeEUDrivingLicence {
		return ""
	}
	if _, ok := ev.DataGroups[dataGroupAAKey]; ok {
		return dataGroupAAKey
	}
	return ""
}

// chipChecksInfo is the server's own verdict on the chip: Passive
// Authentication (the signed data matches what was read and chains to a
// trusted CSCA) and Active/Chip Authentication (the chip holds its private key,
// which a clone does not). Never taken from the app.
type chipChecksInfo struct {
	PassiveAuthentication *passiveAuthInfo `json:"passiveAuthentication,omitempty"`
	ActiveAuthentication  *activeAuthInfo  `json:"activeAuthentication,omitempty"`
	CloneDetected         *bool            `json:"cloneDetected,omitempty"`
	TamperDetected        *bool            `json:"tamperDetected,omitempty"`
}

type passiveAuthInfo struct {
	SODSignatureValid    *bool    `json:"sodSignatureValid,omitempty"`
	DataGroupHashesValid *bool    `json:"dataGroupHashesValid,omitempty"`
	InvalidDataGroups    []string `json:"invalidDataGroups,omitempty"` // e.g. ["DG2"] when a hash mismatched
	CSCATrustChainValid  *bool    `json:"cscaTrustChainValid,omitempty"`
	IssuingCSCA          string   `json:"issuingCsca,omitempty"`
	// DocumentComplete is false when EF.SOD lists a DG14 or DG15 that was not sent;
	// always true for a driving licence.
	DocumentComplete  *bool  `json:"documentComplete,omitempty"`
	DocumentVerifyErr string `json:"documentVerifyErr,omitempty"`
}

// activeAuthInfo: Attempted says the app ran AA/CA; Passed is the proof of
// possession.
type activeAuthInfo struct {
	Attempted *bool  `json:"attempted,omitempty"`
	Passed    *bool  `json:"passed,omitempty"`
	Method    string `json:"method,omitempty"` // "active_authentication" | "chip_authentication"
}

// biometricsInfo is the face check's outcome: the match score against the
// reference (0-1), whether it verified, liveness, and the engine.
type biometricsInfo struct {
	FaceMatchScore *float64 `json:"faceMatchScore,omitempty"`
	FaceVerified   *bool    `json:"faceVerified,omitempty"`
	LivenessResult string   `json:"livenessResult,omitempty"` // "passed" | "failed" | "not_performed"
	// LivenessScore is a liveness confidence, 0..1, when the face provider
	// gives one; nil otherwise (Regula reports liveness as passed or not,
	// and the Yivi method's face check runs no liveness: "not_performed").
	LivenessScore *float64 `json:"livenessScore,omitempty"`
	Engine        string   `json:"engine,omitempty"` // "on_device" | "iris" | "ghostfacenet_tflite" (this server) | "regula"

	// Set by this server's own 1:1 verification (the Yivi method's face
	// check, yivi.go), never by an app's self-report: the threshold the score
	// was held to, how many live frames were evaluated, where the reference
	// face came from ("yivi:pbdf.pbdf.passport.photo"), and which frame
	// integrity checks ran.
	Threshold       *float64       `json:"threshold,omitempty"`
	FramesEvaluated *int           `json:"framesEvaluated,omitempty"`
	ReferenceSource string         `json:"referenceSource,omitempty"`
	Injection       *injectionInfo `json:"injection,omitempty"`
}

// deviceInfo is app and device metadata, released whatever was requested.
type deviceInfo struct {
	AppVersion     string `json:"appVersion,omitempty"`
	DevicePlatform string `json:"devicePlatform,omitempty"` // e.g. "android", "ios"
}

// Result attributes a flow can request (requestedAttributes).
const (
	attrDocument      = "dg1"        // document/MRZ fields, holder identity, validity
	attrDG11          = "dg11"       // DG11 extras: personalNumber, placeOfBirth
	attrDG2           = "dg2"        // raw face image off the chip
	attrFaceImage     = "face_image" // alias for attrDG2
	attrChipChecks    = "chip_checks"
	attrBiometrics    = "biometrics"
	attrSelfie        = "selfie"         // live selfie captured during face verification
	attrDocumentImage = "document_image" // visual (VIZ) capture of the document
	// attrOutcomeOnly is a flow's whole list when the admin chose to release no
	// data: it names no result attribute, so nothing but the outcome (status,
	// assurance) is released.
	attrOutcomeOnly = "outcome_only"
)

// attrRequested reports whether any of keys was requested. An empty
// RequestedAttributes list is unrestricted: only a session no flow governs
// has one, since a flow's session gets requestedAttributesOf the flow.
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

// buildResult is the stored and released result: only what the session
// requested, after the flow's BSN and redaction policy. It is the one place
// evidence becomes a result, so nothing leaves raw. Chip checks are the
// server's own (verifiedChipChecks), never the app's.
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
		// Browsers do not render JPEG2000, which DG2 portraits often are.
		if converted, mime, err := images.ToDisplayablePNG(photo.ImageBase64, photo.MimeType); err != nil {
			slog.Warn("identity proofing: could not convert the photo for display", slog.String("session_id", sess.ID), slog.Any("error", err))
		} else {
			photo.ImageBase64, photo.MimeType = converted, mime
		}
		if released, ok := releasedFace(photo, redaction); ok {
			result["photo"] = released
		} else {
			slog.Warn("identity proofing: could not blur the photo; leaving it out", slog.String("session_id", sess.ID))
		}
	}

	if req.Selfie != nil && attrRequested(sess, attrSelfie) {
		selfie := *req.Selfie
		if converted, mime, err := images.ToDisplayablePNG(selfie.ImageBase64, selfie.MimeType); err != nil {
			slog.Warn("identity proofing: could not convert the selfie for display", slog.String("session_id", sess.ID), slog.Any("error", err))
		} else {
			selfie.ImageBase64, selfie.MimeType = converted, mime
		}
		if released, ok := releasedFace(selfie, redaction); ok {
			result["selfie"] = released
		} else {
			slog.Warn("identity proofing: could not blur the selfie; leaving it out", slog.String("session_id", sess.ID))
		}
	}

	// A face matched against the relying party's own photo (a flow without
	// nfc_read) says so, and releases that photo with the selfie, so whoever
	// reads the result sees both faces that were compared.
	if sess.ReferencePhoto != "" {
		result["faceReference"] = faceReferenceRelyingParty
		if attrRequested(sess, attrSelfie) {
			ref := photoInfo{ImageBase64: sess.ReferencePhoto, MimeType: sess.ReferencePhotoMime}
			if released, ok := releasedFace(ref, redaction); ok {
				result["referencePhoto"] = released
			} else {
				slog.Warn("identity proofing: could not blur the reference photo; leaving it out", slog.String("session_id", sess.ID))
			}
		}
	}

	if attrRequested(sess, attrDocumentImage) {
		if img := releasedDocumentImage(sess, req.DocumentImage, redaction); img != nil {
			result["documentImage"] = img
		}
		if img := releasedDocumentImage(sess, req.DocumentImageBack, redaction); img != nil {
			result["documentImageBack"] = img
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

	// Released whatever was requested, like device: how well the session was
	// verified, not personal data.
	result["assurance"] = computeAssurance(fd, req, verifiedChipChecks, faceMatchSourceOf(sess))
	return result
}

// faceReferenceRelyingParty is a result's faceReference when the face was
// matched against the relying party's own photo rather than the chip's DG2.
const faceReferenceRelyingParty = "relying_party"

// releasedDocumentImage is docImage as the result carries it: its BSN region
// covered under a BlurBSN policy. Nil when there is none, or when the BSN
// could not be covered: the photo is then withheld, never released readable.
func releasedDocumentImage(sess session.Session, docImage *documentImageInfo, redaction privacy.RedactionPolicy) *documentImageInfo {
	if docImage == nil {
		return nil
	}
	out := *docImage
	if redaction.BlurBSN && out.BSNRegion != nil {
		r := redact.Rect{X: out.BSNRegion.X, Y: out.BSNRegion.Y, W: out.BSNRegion.W, H: out.BSNRegion.H}
		blurred, mime, err := redact.Region(out.ImageBase64, out.MimeType, r)
		if err != nil {
			slog.Warn("identity proofing: could not cover the BSN in a document image; withholding it", slog.String("session_id", sess.ID), slog.Any("error", err))
			return nil
		}
		out.ImageBase64, out.MimeType = blurred, mime
	}
	return &out
}

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

// dutchIssuingState is the only issuing state whose DG11 personal number is a
// BSN.
const dutchIssuingState = "NLD"

// applyBSNPolicy applies the BSN policy to doc.PersonalNumber before doc goes
// into any result, whatever was requested. Only Dutch documents: other
// countries' personal numbers are not BSNs. A Dutch number failing the
// 11-proef is dropped whatever the policy.
func applyBSNPolicy(doc *documentInfo, policy privacy.BSNPolicy) {
	if doc.PersonalNumber == "" || doc.IssuingState != dutchIssuingState {
		return
	}
	if !bsn.Masked(doc.PersonalNumber) && !bsn.Valid(doc.PersonalNumber) {
		doc.PersonalNumber = ""
		return
	}
	switch policy.Effective() {
	case privacy.BSNPolicyOmit:
		doc.PersonalNumber = ""
	case privacy.BSNPolicyMask:
		doc.PersonalNumber = bsn.Mask(doc.PersonalNumber)
	case privacy.BSNPolicyRetrieve:
	}
}

// redactBSNFromEvidence drops the raw DG11 from a Dutch document's evidence
// under a mask or omit policy, before it is stored: raw ASN.1 has no masked
// form. Chip verification does not need DG11. It runs even with an empty
// parsed personal number: older chips carry a BSN in DG11 the MRZ does not
// show.
func redactBSNFromEvidence(ev *mrtdEvidenceRequest, doc *documentInfo, policy privacy.BSNPolicy) {
	if ev == nil || doc == nil || doc.IssuingState != dutchIssuingState {
		return
	}
	switch policy.Effective() {
	case privacy.BSNPolicyMask, privacy.BSNPolicyOmit:
		delete(ev.DataGroups, dataGroupPersonalDetails)
	}
}

// verifyMrtdEvidence runs Passive and, where the chip supports it, Active
// Authentication on the raw chip data: the only source of chip checks. Active
// Authentication counts only with the session's own challenge
// (expectedAAChallenge), against relay and replay. An error is malformed
// evidence (a bad request); a check that ran and failed is in the result.
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
	// The chip is trusted only when the whole Passive Authentication holds,
	// document completeness included.
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
			// A response to another nonce than the session's challenge proves nothing
			// about the chip being here now (a replay or relay), however valid.
			active.Passed = false
		}
		method := "chip_authentication"
		if aaKeyDataGroup == dataGroupAAKey {
			method = "active_authentication"
		}
		checks.ActiveAuthentication = &activeAuthInfo{
			Attempted: &active.Attempted,
			Passed:    &active.Passed,
			Method:    method,
		}
		// AA ran and did not verify: copied data without the private key, or a
		// foreign nonce.
		cloneDetected := active.Attempted && !active.Passed
		checks.CloneDetected = &cloneDetected
	}

	return checks, nil
}

// aaChallengeMatches compares the nonce with the session's challenge in
// constant time. An empty or malformed side never matches.
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

// errCodeChipCloneDetected rejects a chip that did not prove it holds its
// private key (authenticityFailure).
const errCodeChipCloneDetected = "CHIP_CLONE_DETECTED"

// authenticityFailure reports a tampered or cloned chip, which rejects the
// session whatever else held. No chip evidence is not a failure.
//
// When fd asks for nfc.chip_auth and the chip carries an AA key, a response
// that is missing counts as cloned too: the app runs AA whenever the check is
// listed, so leaving it out is what a recorded read replayed without the chip
// looks like. A chip whose EF.SOD lists DG15 cannot dodge this by leaving
// DG15 out: Passive Authentication's completeness check marks that tampered.
func authenticityFailure(fd *flow.FlowDefinition, checks *chipChecksInfo) (failed bool, errorCode string) {
	if checks == nil {
		return false, ""
	}
	if checks.TamperDetected != nil && *checks.TamperDetected {
		return true, "DOC_TAMPERED"
	}
	if checks.CloneDetected != nil && *checks.CloneDetected {
		return true, errCodeChipCloneDetected
	}
	if aa := checks.ActiveAuthentication; aa != nil && (aa.Passed == nil || !*aa.Passed) && flowListsCheck(fd, flow.CheckNFCChipAuth) {
		return true, errCodeChipCloneDetected
	}
	return false, ""
}

// flowListsCheck is whether c is among the checks the app is asked to run for
// fd (assuranceChecksFor).
func flowListsCheck(fd *flow.FlowDefinition, c flow.Check) bool {
	return fd != nil && slices.Contains(assuranceChecksFor(fd), c)
}

// ---- relying-party-facing handlers ----------------------------------------

// sessionResultView is the relying party's view of a session's result, already
// minimised by buildResult.
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

// handleAppSession is the app's first call after opening the link: what to
// collect, who asks, how long it has. Opening it marks a created session
// opened; it is idempotent.
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
		writeInternalError(w, r, "could not resolve flow", err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// chipAccessFor is the chip access key a device is handed: only while the
// chip read is the current step, so a device taking the session over later
// never gets the key to the document.
func chipAccessFor(sess session.Session, current *string) *session.ChipAccessKey {
	if current == nil || *current != string(flow.StepNFCRead) || sess.Steps.Document == nil {
		return nil
	}
	return sess.Steps.Document.ChipAccess
}

// buildAppSessionView is the app-facing view of sess for the device holding
// role's slot ("" before anyone claimed the session).
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
	chipAccess := chipAccessFor(sess, current)
	var faceVerification *faceVerificationInfo
	var faceProvider flow.FaceProvider
	if resolvedFlow != nil && selfieLocation == flow.LocationNative && slices.ContainsFunc(steps, isFaceStep) {
		faceProvider = s.faceProviderFor(resolvedFlow)
	}
	if s.cfg.Regula != nil && faceProvider == flow.FaceProviderRegula {
		faceVerification = &faceVerificationInfo{Provider: faceProviderRegula, FaceAPIURL: s.cfg.RegulaFaceAPIPublicURL, Tag: regulaTag(sess)}
		s.queueRegulaSweep(r.Context(), sess)
	}
	var faceReference *photoInfo
	if current != nil && isFaceStep(flow.Step(*current)) && selfieLocation == flow.LocationNative {
		if image, mime, ok := s.faceMatchReference(sess); ok {
			faceReference = &photoInfo{ImageBase64: image, MimeType: mime}
		}
	}
	device := callerDeviceView{Authorized: role != "" || sess.Method == session.MethodBiometricBoundLogin, Role: role}
	if role != "" {
		device.deviceView = s.toDeviceView(sess.Access.Slot(role))
	}
	return appSessionView{
		ID: sess.ID, Method: sess.Method, RelyingParty: s.tenantDisplayName(r.Context(), sess.TenantID), Status: sess.Status, Language: requestLanguage(r, sess.Language),
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

// appSessionChangeKey is what handleAppSessionEvents waits on: status,
// completed steps, resets (a reset from opened changes nothing else), and
// the device slots, so a handover or an inactive device wakes every client.
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

// nativeHandoffPending reports whether the browser should offer the Idem
// app's QR: while nfc_read (and document_capture with it) has not landed, or a
// native face step has not. Without a flow there is no step model, so it is
// offered for an nfc_passport session and never for a bound login.
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

// sessionByPathToken resolves the session of an app-facing route, where the
// path token is the only credential.
func (s *Server) sessionByPathToken(w http.ResponseWriter, r *http.Request) (session.Session, bool) {
	sess, err := s.sessions.Authenticate(r.PathValue("token"))
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return session.Session{}, false
	}
	return sess, true
}

// handleAppSessionResult refuses the single-shot result: every session runs a
// flow, whose evidence arrives step by step.
func (s *Server) handleAppSessionResult(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.appSessionByPathToken(w, r); !ok {
		return
	}
	writeErrorCode(w, http.StatusConflict, errCodeFlowStepsRequired,
		"this session runs a flow: submit each step through POST .../steps/... and then POST .../submit")
}
