package proofingengine

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// submitDocumentStepRequest is document_capture on its own: the MRZ reading,
// sent as soon as the scan completes.
type submitDocumentStepRequest struct {
	Document *documentInfo `json:"document"`
	// ChipAccess is the MRZ-derived chip key, kept until nfc_read lands so a device
	// taking over can go straight to the chip. Optional.
	ChipAccess *session.ChipAccessKey `json:"chipAccess,omitempty"`
}

// handleSubmitDocumentStep stores the MRZ reading as soon as it is scanned. A
// later nfc_read carrying the chip's reading supersedes it.
func (s *Server) handleSubmitDocumentStep(w http.ResponseWriter, r *http.Request) {
	sess, resolvedFlow, caller, ok := s.stepSession(w, r, flow.StepDocumentCapture)
	if !ok {
		return
	}
	var req submitDocumentStepRequest
	if !s.decode(w, r, &req) {
		return
	}
	if req.Document == nil || req.Document.Number == "" {
		writeError(w, http.StatusBadRequest, "document with at least a number is required")
		return
	}
	if req.ChipAccess != nil && !validChipAccessDocumentType(req.ChipAccess.DocumentType) {
		writeError(w, http.StatusBadRequest, "chipAccess.documentType must be passport, identity_card or drivers_license")
		return
	}
	if flow.DrivingLicence(req.Document.Type) || (req.ChipAccess != nil && flow.DrivingLicence(req.ChipAccess.DocumentType)) {
		writeDocumentUnsupported(w)
		return
	}
	// Same BSN guarantee as handleSubmitNFCStep: the policy applies before
	// anything is persisted into Steps.
	bsnPolicy, _ := effectivePrivacyPolicy(resolvedFlow)
	applyBSNPolicy(req.Document, bsnPolicy)
	keepChipAccess := slices.Contains(resolvedFlow.Steps, flow.StepNFCRead)

	updated, err := s.writeStep(sess, caller, flow.StepDocumentCapture, func(sess *session.Session, now time.Time) {
		evidence := documentEvidence(req.Document)
		evidence.Timing = stepTiming(nil, now)
		evidence.Source = session.DocumentSourceMRZ
		if keepChipAccess && sess.Steps.NFC == nil {
			evidence.ChipAccess = req.ChipAccess
		}
		sess.Steps.Document = &evidence
	})
	if err != nil {
		s.writeStepError(w, r, sess, resolvedFlow, caller, flow.StepDocumentCapture, err)
		return
	}
	s.auditStepSubmitted(updated, resolvedFlow, string(flow.StepDocumentCapture), actorDetails(updated, caller.role))
	writeJSON(w, http.StatusOK, s.stepResponseFor(updated, resolvedFlow))
}

// submitDocumentPhotoStepRequest is the document_photo step: the printed data
// page's photo, and where its BSN is when the app found it.
type submitDocumentPhotoStepRequest struct {
	Front *documentPhotoSideRequest `json:"front"`
	// Back is omitted for a document without one worth taking (a passport).
	Back *documentPhotoSideRequest `json:"back,omitempty"`
}

type documentPhotoSideRequest struct {
	Image     string       `json:"image"`
	MimeType  string       `json:"mimeType,omitempty"`
	BSNRegion *imageRegion `json:"bsnRegion,omitempty"`
}

// handleSubmitDocumentPhotoStep stores the document's front and optional back,
// released as documentImage and documentImageBack with the BSN covered under a
// BlurBSN policy, in the result and in storage.
func (s *Server) handleSubmitDocumentPhotoStep(w http.ResponseWriter, r *http.Request) {
	sess, resolvedFlow, caller, ok := s.stepSession(w, r, flow.StepDocumentPhoto)
	if !ok {
		return
	}
	var req submitDocumentPhotoStepRequest
	if !s.decode(w, r, &req) {
		return
	}
	if req.Front == nil {
		writeError(w, http.StatusBadRequest, "front is required")
		return
	}
	front, err := documentPhotoSide("front", *req.Front)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var back *session.DocumentPhotoSide
	if req.Back != nil {
		side, err := documentPhotoSide("back", *req.Back)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		back = &side
	}

	updated, err := s.writeStep(sess, caller, flow.StepDocumentPhoto, func(sess *session.Session, now time.Time) {
		sess.Steps.DocumentPhoto = &session.DocumentPhotoStepEvidence{Front: front, Back: back, Timing: stepTiming(nil, now)}
	})
	if err != nil {
		s.writeStepError(w, r, sess, resolvedFlow, caller, flow.StepDocumentPhoto, err)
		return
	}
	s.auditStepSubmitted(updated, resolvedFlow, string(flow.StepDocumentPhoto), actorDetails(updated, caller.role))
	writeJSON(w, http.StatusOK, s.stepResponseFor(updated, resolvedFlow))
}

func documentPhotoSide(name string, in documentPhotoSideRequest) (session.DocumentPhotoSide, error) {
	if in.Image == "" {
		return session.DocumentPhotoSide{}, fmt.Errorf("%s.image is required", name)
	}
	raw, mime, err := decodeImageBase64(in.Image, firstNonEmpty(in.MimeType, defaultImageMime))
	if err != nil {
		return session.DocumentPhotoSide{}, fmt.Errorf("invalid %s.image: %w", name, err)
	}
	side := session.DocumentPhotoSide{Image: base64.StdEncoding.EncodeToString(raw), MimeType: mime}
	if r := in.BSNRegion; r != nil {
		if !validImageRegion(*r) {
			return session.DocumentPhotoSide{}, fmt.Errorf("%s.bsnRegion must lie within the image (x, y, w, h in [0,1])", name)
		}
		side.BSNRegion = &session.ImageRegion{X: r.X, Y: r.Y, W: r.W, H: r.H}
	}
	return side, nil
}

// validImageRegion is whether r is a non-empty box within the image.
func validImageRegion(r imageRegion) bool {
	return r.X >= 0 && r.Y >= 0 && r.W > 0 && r.H > 0 && r.X+r.W <= 1 && r.Y+r.H <= 1
}

// errCodeDocumentUnsupported refuses an EU driving licence at
// document_capture and nfc_read: there is no CSCA source for licences
// (mrtdverify.DrivingLicenceCertPool is empty), so a genuine one could only
// end DOC_TAMPERED.
const errCodeDocumentUnsupported = "document_unsupported"

func writeDocumentUnsupported(w http.ResponseWriter) {
	writeErrorCode(w, http.StatusUnprocessableEntity, errCodeDocumentUnsupported,
		"EU driving licences are not supported yet: scan a passport or identity card")
}

// The document types a chip access key may name.
const (
	chipAccessPassport     = "passport"
	chipAccessIdentityCard = "identity_card"
)

func validChipAccessDocumentType(t string) bool {
	return t == chipAccessPassport || t == chipAccessIdentityCard || t == flow.DocumentTypeDrivingLicence
}

// documentEvidence is document_capture's evidence from the app's MRZ reading.
func documentEvidence(doc *documentInfo) session.DocumentStepEvidence {
	if doc == nil {
		return session.DocumentStepEvidence{}
	}
	return session.DocumentStepEvidence{
		Parsed: session.ParsedMRZ{
			DocumentType: doc.Type, IssuingState: doc.IssuingState, Number: doc.Number,
			Nationality: doc.Nationality, Surname: doc.LastName, GivenNames: doc.FirstName,
			Sex: doc.Sex, DateOfBirth: doc.DateOfBirth, DateOfExpiry: doc.DateOfExpiry,
			PersonalNumber: doc.PersonalNumber,
			// No MRZ checksums run here: Passive Authentication vouches for this data, so
			// true means nothing failed.
			AllChecksValid: true,
		},
	}
}

func documentInfoFromParsedMRZ(p session.ParsedMRZ, now time.Time) documentInfo {
	valid := p.AllChecksValid
	notExpired := !documentExpired(p.DateOfExpiry, now)
	return documentInfo{
		Type: p.DocumentType, Number: p.Number, IssuingState: p.IssuingState, Nationality: p.Nationality,
		FirstName: p.GivenNames, LastName: p.Surname, DisplayName: displayName(p.GivenNames, p.Surname),
		Sex: p.Sex, DateOfBirth: p.DateOfBirth, DateOfExpiry: p.DateOfExpiry, PersonalNumber: p.PersonalNumber,
		Validity: &documentValidityInfo{
			DocumentNumberCheckDigitValid: &valid, DateOfBirthCheckDigitValid: &valid,
			DateOfExpiryCheckDigitValid: &valid, CompositeCheckDigitValid: &valid, NotExpired: &notExpired,
		},
	}
}

func displayName(given, surname string) string {
	if given == "" && surname == "" {
		return ""
	}
	return given + " " + surname
}

// documentExpired reports whether a YYYY-MM-DD expiry date has passed on now's
// date (UTC): a document is valid through its expiry day. An unreadable date
// is expired.
func documentExpired(expiryDate string, now time.Time) bool {
	expiry, err := time.Parse(time.DateOnly, expiryDate)
	if err != nil {
		return true
	}
	return expiry.Before(now.UTC().Truncate(day))
}
