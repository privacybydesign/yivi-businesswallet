package proofingengine

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
	pp "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// errStepsIncomplete: a submit before every step has evidence, or a reset while
// the result was being built.
var errStepsIncomplete = errors.New("not every step of this session has a result yet")

// errDocumentPhotoMissing: a submit with every app step done but the browser's
// document photo (SubmitDocumentPhoto) not in yet.
var errDocumentPhotoMissing = errors.New("the document photo is taken in the browser and has not come in yet")

// handleSubmitSession lets any device of the session submit it once every step
// has evidence; only then does it get its outcome. A repeat answers the
// current state.
func (s *Server) handleSubmitSession(w http.ResponseWriter, r *http.Request) {
	sess, caller, ok := s.appSessionByPathToken(w, r)
	if !ok {
		return
	}
	resolvedFlow, err := s.resolveSessionFlow(r.Context(), sess)
	if err != nil {
		writeInternalError(w, r, "could not resolve flow", err)
		return
	}
	if resolvedFlow == nil {
		writeError(w, http.StatusConflict, "this session has no resolved flow definition")
		return
	}
	updated, err := s.finishSession(r.Context(), sess, resolvedFlow, caller)
	switch {
	case errors.Is(err, errSessionComplete):
		if current, getErr := s.sessions.Get(sess.TenantID, sess.ID); getErr == nil {
			sess = current
		}
		resp := s.stepResponseFor(sess, resolvedFlow)
		resp.AlreadyRecorded = true
		writeJSON(w, http.StatusOK, resp)
	case errors.Is(err, errDocumentPhotoMissing):
		writeErrorCode(w, http.StatusConflict, pp.CodeDocumentPhotoMissing, err.Error())
	case errors.Is(err, errStepsIncomplete):
		writeErrorCode(w, http.StatusConflict, errCodeStepsIncomplete, err.Error())
	case accessErrorCode(err) != "":
		s.denyApp(w, r, sess, caller, err)
	case err != nil:
		writeInternalError(w, r, "could not finish session", err)
	default:
		writeJSON(w, http.StatusOK, s.stepResponseFor(updated, resolvedFlow))
	}
}

// finishSession decides the outcome of a session whose steps all have
// evidence: the server, not the app, decides. The write re-checks the caller's
// slot, the evidence and that the session was not reset meanwhile.
func (s *Server) finishSession(ctx context.Context, sess session.Session, fd *flow.FlowDefinition, caller appCaller) (session.Session, error) {
	if err := checkAppWrite(&sess, caller); err != nil {
		return sess, err
	}
	if !requiredStepsComplete(sess, fd) {
		if documentPhotoMissing(sess, fd) {
			return sess, errDocumentPhotoMissing
		}
		return sess, errStepsIncomplete
	}

	req := appResultRequest{Status: session.StatusApproved}
	var verifiedChecks *chipChecksInfo
	if p := sess.Steps.DocumentPhoto; p != nil {
		req.DocumentImage = documentImageFromSide(&p.Front)
		req.DocumentImageBack = documentImageFromSide(p.Back)
	}
	if sess.Steps.Document != nil {
		doc := documentInfoFromParsedMRZ(sess.Steps.Document.Parsed, s.cfg.Now())
		req.Document = &doc
	} else if p := sess.Steps.DocumentPhoto; p != nil && p.Check != nil {
		// No MRZ or chip read: what the document scan read is the document,
		// and its age answer the session's.
		req.Document = scannedDocumentInfo(p.Check.Document, s.cfg.Now())
		req.AgeOver = p.AgeOver
	}
	if sess.Steps.Selfie != nil {
		// A Regula selfie without a crop (no match ran, or no face found) has no
		// image.
		if sess.Steps.Selfie.Image != "" {
			if raw, mime, err := decodeImageBase64(sess.Steps.Selfie.Image, firstNonEmpty(sess.Steps.Selfie.MimeType, defaultImageMime)); err == nil {
				req.Selfie = &photoInfo{ImageBase64: base64.StdEncoding.EncodeToString(raw), MimeType: mime}
			}
		}
		// Liveness ran on every face submission, so a failure is "failed", not
		// "not_performed".
		req.Biometrics = &biometricsInfo{
			FaceMatchScore: sess.Steps.Selfie.FaceMatchScore, FaceVerified: sess.Steps.Selfie.FaceVerified,
			LivenessResult: livenessResults[sess.Steps.Selfie.LivenessPassed], LivenessScore: sess.Steps.Selfie.LivenessScore,
			Engine: selfieEngine(sess.Steps.Selfie),
		}
	}
	if sess.Steps.NFC != nil {
		nfcReq, err := decodeNFCStepRequest(sess.Steps.NFC.Raw)
		if err != nil {
			return sess, err
		}
		req.MrtdEvidence = nfcReq.MrtdEvidence
		req.Photo = nfcReq.Photo
		req.Device = nfcReq.Device
		checks, err := s.finishChipChecks(sess.Steps.NFC, req.MrtdEvidence, sess.AAChallenge)
		if err != nil {
			return sess, err
		}
		verifiedChecks = checks
		req.AgeOver = sess.Steps.NFC.AgeOver
		if nfcReq.Document != nil && req.Document != nil {
			// The DG11 extras, and its name over the MRZ's.
			req.Document.PersonalNumber = firstNonEmpty(nfcReq.Document.PersonalNumber, req.Document.PersonalNumber)
			req.Document.PlaceOfBirth = nfcReq.Document.PlaceOfBirth
			if nfcReq.Document.DisplayName != "" {
				req.Document.DisplayName = nfcReq.Document.DisplayName
			}
		}
	}

	achieved := computeEIDASAssuranceLevel(fd, req, verifiedChecks, faceMatchSourceOf(sess))
	finalStatus, finalErrorCode, reason := sessionOutcome(fd, sess, req, verifiedChecks, achieved, s.cfg.Now())

	// A session that waits for review keeps its face photos until it is
	// decided (heldForReview); DecideReview applies the flow's policy then.
	bsnPolicy, redactionPolicy := effectivePrivacyPolicy(fd)
	redactionPolicy = heldForReview(finalStatus, redactionPolicy)
	result := buildResult(sess, req, verifiedChecks, fd, bsnPolicy, redactionPolicy)

	snapshotFaceAttempts := faceStepAttempts(sess.Steps.Selfie)
	updated, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		if err := checkAppWrite(sess, caller); err != nil {
			return err
		}
		// The result was built from the snapshot above: it is not this session's
		// evidence any more once a face retry replaced the face evidence.
		if faceStepAttempts(sess.Steps.Selfie) != snapshotFaceAttempts || !requiredStepsComplete(*sess, fd) {
			return errStepsIncomplete
		}
		now := time.Now().UTC()
		if err := advanceToInProgress(sess, now); err != nil {
			return err
		}
		if err := sess.SetStatus(finalStatus, now); err != nil {
			return err
		}
		sess.ErrorCode = finalErrorCode
		sess.Result = result
		// The result is built; only now may the stored evidence be redacted
		// (redactStepsForStorage).
		redactStepsForStorage(sess, bsnPolicy, redactionPolicy)
		return nil
	})
	if err != nil {
		return sess, err
	}
	submitted := actorDetails(updated, caller.role)
	submitted[detailStage] = "session_submit"
	s.auditProofing(updated, eventResultSubmitted, submitted)
	outcomeDetails := map[string]any{}
	if updated.ErrorCode != "" {
		outcomeDetails[detailErrorCode] = updated.ErrorCode
	}
	if reason != "" {
		outcomeDetails[detailReason] = reason
	}
	s.auditProofing(updated, eventTypeForStatus(updated.Status), outcomeDetails)
	// The app is done, whatever the outcome: its liveness attempts are swept
	// from now, not from the session's expiry.
	s.queueRegulaSweep(ctx, updated)
	return updated, nil
}

// Audit reasons of a rejection (sessionOutcome).
const (
	reasonTamperDetected      = "tamper_detected"
	reasonFlowPolicyViolation = "flow_policy_violation"
	reasonCheckFailed         = "check_failed"
	reasonAssuranceNotMet     = "assurance_not_met"
	reasonDocumentDoubtful    = "document_doubtful"
)

// documentPhotoOutcome is what sess's document scan decides: errorCode rejects
// the session (a fake document, a field that failed its check, or a document
// other than the one the session read: documentNumber, when known), and
// review sends it to a person (a doubtful document, zones that read
// differently, or a session the browser ran alone without an authentic
// verdict). Next to an app's own evidence a photo without a check decides
// nothing.
func documentPhotoOutcome(sess session.Session, documentNumber string) (errorCode string, review bool) {
	p := sess.Steps.DocumentPhoto
	if p == nil || p.Check == nil {
		return "", p != nil && sess.Method == session.MethodBrowser
	}
	c := p.Check
	switch {
	case pp.DocumentVerdict(c.Verdict) == pp.DocumentFake:
		return errCodeDocNotAuthentic, false
	case c.FieldsValid != nil && !*c.FieldsValid:
		return errCodeDocFieldsInvalid, false
	case documentNumber != "" && c.Document.Number != "" && !sameDocumentNumber(documentNumber, c.Document.Number):
		return errCodeDocMismatch, false
	}
	return "", pp.DocumentVerdict(c.Verdict) != pp.DocumentAuthentic || (c.ZonesMatch != nil && !*c.ZonesMatch)
}

// sameDocumentNumber is whether a and b are one document number, read from
// different sources: case and spacing aside.
func sameDocumentNumber(a, b string) bool {
	norm := func(s string) string { return strings.ToUpper(strings.Join(strings.Fields(s), "")) }
	return norm(a) == norm(b)
}

// scannedDocumentInfo is what the document scan read off the document, as a
// result's document: the session's own document when it read no MRZ or chip.
func scannedDocumentInfo(d session.ScannedDocument, now time.Time) *documentInfo {
	if d.Number == "" && d.Surname == "" && d.DateOfBirth == "" {
		return nil
	}
	notExpired := !documentExpired(d.DateOfExpiry, now)
	return &documentInfo{
		Kind: d.Kind, Number: d.Number, IssuingState: d.IssuingState, Nationality: d.Nationality,
		FirstName: d.GivenNames, LastName: d.Surname, DisplayName: displayName(d.GivenNames, d.Surname),
		Sex: d.Sex, DateOfBirth: d.DateOfBirth, DateOfExpiry: d.DateOfExpiry, PersonalNumber: d.PersonalNumber,
		Validity: &documentValidityInfo{NotExpired: &notExpired},
	}
}

// sessionOutcome decides a finished session of fd: rejected for a tampered or
// cloned chip or a fake document, a submission outside what fd accepts, a face
// step that did not verify the person, or an achieved eIDAS level below fd's
// required one, in that order; in review for a doubtful document scan
// (documentPhotoOutcome); approved otherwise. The required level is only that minimum: it
// neither raises nor caps achieved, which is computed without it.
func sessionOutcome(fd *flow.FlowDefinition, sess session.Session, req appResultRequest, checks *chipChecksInfo, achieved flow.AssuranceLevel, now time.Time) (status session.Status, errorCode, reason string) {
	if failed, code := authenticityFailure(fd, checks); failed {
		return session.StatusRejected, code, reasonTamperDetected
	}
	var mrzNumber string
	if sess.Steps.Document != nil {
		mrzNumber = sess.Steps.Document.Parsed.Number
	}
	scanCode, doubtful := documentPhotoOutcome(sess, mrzNumber)
	if scanCode != "" {
		return session.StatusRejected, scanCode, reasonTamperDetected
	}
	if failed, code := flowComplianceFailure(fd, sess, req, now); failed {
		return session.StatusRejected, code, reasonFlowPolicyViolation
	}
	// An age check the evidence did not answer (a scan that read no date of
	// birth) is refused, as a Yivi disclosure without the age claim is.
	if asksAge(fd) && req.AgeOver == nil {
		return session.StatusRejected, errCodeAgeNotDisclosed, reasonFlowPolicyViolation
	}
	if failed, code := faceStepFailure(fd, req, checks); failed {
		return session.StatusRejected, code, reasonCheckFailed
	}
	if !flow.MeetsLevel(achieved, fd.RequiredAssuranceLevel) {
		return session.StatusRejected, errCodeAssuranceNotMet, reasonAssuranceNotMet
	}
	if doubtful {
		return session.StatusNeedsReview, "", reasonDocumentDoubtful
	}
	return session.StatusApproved, "", ""
}
