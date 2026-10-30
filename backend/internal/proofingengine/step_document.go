package proofingengine

import (
	"net/http"
	"slices"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/mrtdverify"
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
		writeError(w, http.StatusBadRequest, "chipAccess.documentType must be passport, identity_card, residence_permit or drivers_license")
		return
	}
	if !drivingLicenceSupported(req.Document.Type, req.Document.IssuingState) ||
		(req.ChipAccess != nil && !drivingLicenceSupported(req.ChipAccess.DocumentType, req.ChipAccess.CountryCode)) {
		writeDocumentUnsupported(w)
		return
	}
	// Same BSN guarantee as handleSubmitNFCStep: the policy applies before
	// anything is persisted into Steps.
	bsnPolicy, _ := effectivePrivacyPolicy(resolvedFlow)
	applyBSNPolicy(req.Document, bsnPolicy)
	// An age-check flow stores no date of birth, nor the chip access key made
	// from it: the app opens the chip with its own scan, and scans again after
	// a restart.
	if asksAge(resolvedFlow) {
		req.Document.DateOfBirth = ""
	}
	keepChipAccess := slices.Contains(resolvedFlow.Steps, flow.StepNFCRead) && !asksAge(resolvedFlow)

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

// errCodeDocumentUnsupported refuses a non-Dutch EU driving licence at
// document_capture and nfc_read: Config.DrivingLicenceCertPool holds only
// the Dutch licence CSCAs, so a genuine one could only end DOC_TAMPERED.
const errCodeDocumentUnsupported = "document_unsupported"

func writeDocumentUnsupported(w http.ResponseWriter) {
	writeErrorCode(w, http.StatusUnprocessableEntity, errCodeDocumentUnsupported,
		"only Dutch driving licences are supported: scan a passport, identity card, residence permit or Dutch driving licence")
}

// drivingLicenceSupported is false for a driving licence from a state other
// than mrtdverify.DrivingLicenceIssuingState; any other document passes. An
// issuing state not yet known passes too: the chip's own DG1 decides at
// nfc_read.
func drivingLicenceSupported(documentType, issuingState string) bool {
	return !flow.DrivingLicence(documentType) || issuingState == "" || issuingState == mrtdverify.DrivingLicenceIssuingState
}

// validChipAccessDocumentType is whether t is a document type a chip access
// key may name.
func validChipAccessDocumentType(t string) bool {
	return t == flow.DocumentKindPassport || t == flow.DocumentKindIdentityCard || t == flow.DocumentKindResidencePermit ||
		t == flow.DocumentTypeDrivingLicence
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
			FullSurname: doc.FullLastName, FullGivenNames: doc.FullFirstName,
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
	// The display name is DG11's when the chip has one, as at the chip read.
	shownGiven, shownSurname := p.GivenNames, p.Surname
	if p.FullSurname != "" {
		shownGiven, shownSurname = p.FullGivenNames, p.FullSurname
	}
	return documentInfo{
		Type: p.DocumentType, Kind: flow.DocumentKindOf(p.DocumentType, p.IssuingState, p.Nationality), Number: p.Number, IssuingState: p.IssuingState, Nationality: p.Nationality,
		FirstName: p.GivenNames, LastName: p.Surname, DisplayName: displayName(shownGiven, shownSurname),
		FullFirstName: p.FullGivenNames, FullLastName: p.FullSurname,
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
