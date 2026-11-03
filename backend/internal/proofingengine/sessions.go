// The app's view of a session and its long-poll, result building (data
// minimisation, BSN policy, redaction), chip verification and assurance
// scoring. The relying-party side is rp.go; device access is device_access.go.
package proofingengine

import (
	"context"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
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
	details := map[string]any{detailStage: stage, "stepState": stepState}
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
// default (BSN left out, no face photos kept, nothing covered).
func effectivePrivacyPolicy(resolvedFlow *flow.FlowDefinition) (privacy.BSNPolicy, privacy.RedactionPolicy) {
	bsnPolicy, redaction := privacy.DefaultBSNPolicy, privacy.RedactionPolicy{}
	if resolvedFlow != nil {
		if resolvedFlow.BSNPolicy != "" {
			bsnPolicy = resolvedFlow.BSNPolicy
		}
		if resolvedFlow.KeepFacePhotos != nil {
			redaction.KeepFacePhotos = *resolvedFlow.KeepFacePhotos
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

// Rejection codes of a face step that did not verify the person
// (faceStepFailure), an expired document or a missing face step
// (flowComplianceFailure) and an achieved eIDAS level below the flow's
// required one (sessionOutcome). A document scan (documentPhotoOutcome)
// rejects with errCodeDocNotAuthentic for a document judged fake,
// errCodeDocFieldsInvalid for a field that failed its check and
// errCodeDocMismatch for a document other than the one the session read.
const (
	errCodeDocExpired          = "DOC_EXPIRED"
	errCodeDocNotAuthentic     = "DOC_NOT_AUTHENTIC"
	errCodeDocFieldsInvalid    = "DOC_FIELDS_INVALID"
	errCodeDocMismatch         = "DOCUMENT_MISMATCH"
	errCodeDocTypeRefused      = "DOCUMENT_TYPE_NOT_ACCEPTED"
	errCodeCountryRefused      = "DOCUMENT_COUNTRY_NOT_ACCEPTED"
	errCodeLivenessFailed      = "LIVENESS_FAILED"
	errCodeFaceNoMatch         = "FACE_NO_MATCH"
	errCodeAssuranceNotMet     = "ASSURANCE_NOT_MET"
	errCodeFaceStepNotComplete = "FACE_STEP_NOT_COMPLETED"
	errCodeAgeNotDisclosed     = "AGE_NOT_DISCLOSED"
)

// hasFaceStep reports whether fd has any of the face steps the one selfie
// submission fulfils.
func hasFaceStep(fd *flow.FlowDefinition) bool {
	return slices.ContainsFunc(fd.Steps, isFaceStep)
}

// faceStepFailure reports a face step that did not verify the person:
//   - a capture that was not live, whatever fd lists: Regula, the only face
//     provider, runs liveness on every capture, and a flow without a face
//     match (liveness or a selfie alone) has nothing else to fail on;
//   - in a step that compares faces, no passing match, at any level.
func faceStepFailure(fd *flow.FlowDefinition, req appResultRequest, checks *chipChecksInfo) (failed bool, errorCode string) {
	if fd == nil || !hasFaceStep(fd) {
		return false, ""
	}
	if checkOutcome(fd, flow.CheckFaceLiveness, req, checks) == checkStateFail {
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
		if !fd.AcceptsDocumentType(req.Document.Type, req.Document.Kind) {
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
		return true, errCodeFaceStepNotComplete
	}
	return false, ""
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
	// AgeOver is the flow's age answered from the chip's date of birth.
	AgeOver *ageOverInfo `json:"ageOver,omitempty"`
}

// documentInfo is the identity read off the document's MRZ/DG1, plus the DG11
// extras. The shape follows vcmrtd's PassportMRZ/PassportData.
type documentInfo struct {
	Type         string `json:"type,omitempty"` // ICAO document code, e.g. "P" for passport
	Kind         string `json:"kind,omitempty"` // flow.DocumentKindOf, e.g. flow.DocumentKindResidencePermit
	Number       string `json:"number,omitempty"`
	IssuingState string `json:"issuingState,omitempty"`
	Nationality  string `json:"nationality,omitempty"`
	FirstName    string `json:"firstName,omitempty"`
	LastName     string `json:"lastName,omitempty"`
	// FullFirstName and FullLastName are DG11's name, when the chip has one:
	// UTF-8, and never cut off, where the MRZ's fixed-width field truncates a
	// long name (30 characters on an ID card).
	FullFirstName string `json:"fullFirstName,omitempty"`
	FullLastName  string `json:"fullLastName,omitempty"`
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
// is where buildResult covers it under a BlurBSN, mask or omit policy. Without it the photo
// is withheld; the server never guesses where the BSN is.
type documentImageInfo struct {
	ImageBase64 string       `json:"imageBase64,omitempty"`
	MimeType    string       `json:"mimeType,omitempty"`
	BSNRegion   *imageRegion `json:"bsnRegion,omitempty"`
	// BSNRedacted is the scan service having blacked out the BSN itself.
	BSNRedacted bool `json:"-"`
}

// biometricsInfo is the face check's outcome: the match score against the
// reference (0-1), whether it verified, liveness, and the engine.
type biometricsInfo struct {
	FaceMatchScore *float64 `json:"faceMatchScore,omitempty"`
	FaceVerified   *bool    `json:"faceVerified,omitempty"`
	LivenessResult string   `json:"livenessResult,omitempty"` // "passed" | "failed" | "not_performed"
	// LivenessScore is a liveness confidence, 0..1, when the face provider
	// gives one; nil otherwise (Regula reports liveness as passed or not).
	LivenessScore *float64 `json:"livenessScore,omitempty"`
	Engine        string   `json:"engine,omitempty"` // "on_device" | "iris" | "ghostfacenet_tflite" (this server) | "regula"
	// Threshold is the score the match was held to.
	Threshold *float64 `json:"threshold,omitempty"`
}

// deviceInfo is app and device metadata, released whatever was requested.
type deviceInfo struct {
	AppVersion     string `json:"appVersion,omitempty"`
	DevicePlatform string `json:"devicePlatform,omitempty"` // e.g. "android", "ios"
}
