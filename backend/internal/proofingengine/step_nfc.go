package proofingengine

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// submitNFCStepRequest is the nfc_read step as the Idem app posts it. Document
// is the app's own reading and is never trusted: handleSubmitNFCStep reads the
// document from the evidence's DG1/DG11, the bytes Passive Authentication
// checks. The field stays so the request still decodes.
type submitNFCStepRequest struct {
	Photo        *photoInfo           `json:"photo,omitempty"`
	Document     *documentInfo        `json:"document,omitempty"`
	MrtdEvidence *mrtdEvidenceRequest `json:"mrtdEvidence"`
	Device       *deviceInfo          `json:"device,omitempty"`
}

func (s *Server) handleSubmitNFCStep(w http.ResponseWriter, r *http.Request) {
	sess, resolvedFlow, caller, ok := s.stepSession(w, r, flow.StepNFCRead)
	if !ok {
		return
	}
	var req submitNFCStepRequest
	if !s.decode(w, r, &req) {
		return
	}
	if req.MrtdEvidence == nil {
		writeError(w, http.StatusBadRequest, "mrtdEvidence is required")
		return
	}
	checks, err := s.verifyMrtdEvidence(req.MrtdEvidence, sess.AAChallenge)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid mrtdEvidence: "+err.Error())
		return
	}
	// The BSN policy may drop DG11 below, so the finish never re-checks it.
	if slices.Contains(checks.PassiveAuthentication.InvalidDataGroups, dataGroupPersonalDetails) {
		writeError(w, http.StatusBadRequest, "invalid mrtdEvidence: DG11 does not match EF.SOD")
		return
	}
	// The document is read off the chip evidence, never taken from the
	// app's own copy: see documentFromEvidence.
	chipDoc, err := documentFromEvidence(req.MrtdEvidence, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid mrtdEvidence: "+err.Error())
		return
	}
	if !drivingLicenceSupported(chipDoc.Type, chipDoc.IssuingState) {
		writeDocumentUnsupported(w)
		return
	}
	req.Document = chipDoc
	// The photo is the face_match reference (faceMatchReference), so it must
	// be the chip's own portrait: see photoFromChip.
	if req.Photo != nil && !photoFromChip(req.Photo, req.MrtdEvidence) {
		writeError(w, http.StatusBadRequest, "photo is not the portrait in mrtdEvidence's "+portraitDataGroup(req.MrtdEvidence))
		return
	}
	// Without it a flow that matches the face against the chip could never
	// finish: the face step would wait for a reference that never comes.
	if req.Photo == nil && sess.ReferencePhoto == "" && flowNeedsFaceMatch(resolvedFlow) {
		writeError(w, http.StatusBadRequest, "photo is required: the face step matches against the chip's portrait")
		return
	}
	// Apply the BSN policy before anything is stored: to the parsed number, and to
	// the raw DG11, which carries the same BSN and is stored with the evidence.
	// Older Dutch chips have a BSN in DG11 even when the MRZ shows none. Without
	// any document to tell the issuing state, DG11 is dropped under a mask or omit
	// policy rather than kept possibly readable. The raw DG1, whose MRZ holds the
	// BSN on older Dutch documents too, stays until the finish: it re-runs
	// Passive Authentication, which needs DG1, and then drops it
	// (redactStepsForStorage). Until then it is sealed with the session, and an
	// expired or cancelled session keeps none of its steps.
	bsnPolicy, _ := effectivePrivacyPolicy(resolvedFlow)
	switch {
	case req.Document != nil:
		applyBSNPolicy(req.Document, bsnPolicy)
		redactBSNFromEvidence(req.MrtdEvidence, req.Document, bsnPolicy)
	case sess.Steps.Document != nil && sess.Steps.Document.Parsed.IssuingState != "":
		redactBSNFromEvidence(req.MrtdEvidence, &documentInfo{IssuingState: sess.Steps.Document.Parsed.IssuingState}, bsnPolicy)
	default:
		if p := bsnPolicy.Effective(); p == privacy.BSNPolicyMask || p == privacy.BSNPolicyOmit {
			delete(req.MrtdEvidence.DataGroups, dataGroupPersonalDetails)
		}
	}
	// An age-check flow keeps the answer and the chip checks, never the date
	// of birth: the raw DG1 the finish would check again goes with it.
	var ageOver *ageOverInfo
	var chipChecks map[string]any
	if asksAge(resolvedFlow) {
		ageOver = ageOverOf(req.Document, resolvedFlow, s.cfg.Now())
		if chipChecks, err = marshalToMap(checks); err != nil {
			writeInternalError(w, r, "could not store nfc evidence", err)
			return
		}
		withholdBirthDate(&req)
	}
	raw, err := marshalToMap(req)
	if err != nil {
		writeInternalError(w, r, "could not store nfc evidence", err)
		return
	}

	hasDocumentCapture := slices.Contains(resolvedFlow.Steps, flow.StepDocumentCapture)
	updated, err := s.writeStep(sess, caller, flow.StepNFCRead, func(sess *session.Session, now time.Time) {
		sess.Steps.NFC = &session.NFCStepEvidence{Raw: raw, ChipChecks: chipChecks, AgeOver: ageOver, Timing: stepTiming(nil, now)}
		if !hasDocumentCapture {
			return
		}
		// This submission also fulfils document_capture, and its document, backed by
		// the chip evidence, supersedes an MRZ-only one. The chip access key is no
		// longer needed.
		switch {
		case req.Document != nil || sess.Steps.Document == nil:
			var existing *session.StepTiming
			if sess.Steps.Document != nil {
				existing = &sess.Steps.Document.Timing
			} else {
				sess.Access.RecordStep(caller.role, string(flow.StepDocumentCapture))
			}
			evidence := documentEvidence(req.Document)
			evidence.Timing = stepTiming(existing, now)
			evidence.Source = session.DocumentSourceChip
			sess.Steps.Document = &evidence
		default:
			// A copy, not a write through the pointer: the in-memory store's Update works
			// on a shallow copy.
			doc := *sess.Steps.Document
			doc.ChipAccess = nil
			sess.Steps.Document = &doc
		}
	})
	if err != nil {
		s.writeStepError(w, r, sess, resolvedFlow, caller, flow.StepNFCRead, err)
		return
	}
	s.auditStepSubmitted(updated, resolvedFlow, "nfc_read", actorDetails(updated, caller.role))
	writeJSON(w, http.StatusOK, s.stepResponseFor(updated, resolvedFlow))
}

// photoFromChip reports whether photo's bytes are inside ev's portrait data
// group. Passive Authentication covers it but not the separately sent photo;
// without this a genuine chip could be paired with any face.
func photoFromChip(photo *photoInfo, ev *mrtdEvidenceRequest) bool {
	raw, _, err := decodeImageBase64(photo.ImageBase64, photo.MimeType)
	if err != nil || len(raw) == 0 {
		return false
	}
	portrait, err := hex.DecodeString(ev.DataGroups[portraitDataGroup(ev)])
	return err == nil && bytes.Contains(portrait, raw)
}

// portraitDataGroup is where ev's chip keeps the holder's portrait: DG2, or a
// driving licence's DG6.
func portraitDataGroup(ev *mrtdEvidenceRequest) string {
	if ev.DocumentType == documentTypeEUDrivingLicence {
		return dataGroupDLPortrait
	}
	return dataGroupPortrait
}

// finishChipChecks are the chip checks the finish scores: those kept at the
// chip read (an age-check flow), else run again on the stored evidence ev.
func (s *Server) finishChipChecks(nfc *session.NFCStepEvidence, ev *mrtdEvidenceRequest, aaChallenge string) (*chipChecksInfo, error) {
	if nfc.ChipChecks == nil {
		return s.verifyMrtdEvidence(ev, aaChallenge)
	}
	data, err := json.Marshal(nfc.ChipChecks)
	if err != nil {
		return nil, err
	}
	var checks chipChecksInfo
	if err := json.Unmarshal(data, &checks); err != nil {
		return nil, err
	}
	return &checks, nil
}

func decodeNFCStepRequest(raw map[string]any) (submitNFCStepRequest, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return submitNFCStepRequest{}, err
	}
	var req submitNFCStepRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return submitNFCStepRequest{}, err
	}
	return req, nil
}

func marshalToMap(v any) (map[string]any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}
