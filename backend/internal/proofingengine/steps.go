// The Idem app's step endpoints (/api/v1/app/{token}/steps/...): a session
// collects its evidence one step at a time, so a device that takes the
// session over resumes where the last one stopped. A step never finishes the
// session: once every step of the flow has evidence the views say
// readyToSubmit, and the session gets its outcome only on POST .../submit
// (handleSubmitSession), decided by the server (finishSession) -
// authenticityFailure/flowComplianceFailure can only push a would-be approval
// to rejected, never the other way around.
//
// A session needs a resolved flow for these; one without uses the legacy
// POST .../result. document_capture (the MRZ the app scanned) and nfc_read
// (the chip) always come together; the app's camera reads the MRZ, there is
// no OCR here. The face step runs in the app against Regula.
package proofingengine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/images"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/redact"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// stepSession resolves the app-facing token to a session governed by a
// resolved flow that can still take step - every step endpoint's shared
// precondition, checked with stepWriteCheck on this snapshot so a doomed
// request fails before any processing. The write itself checks again (see
// writeStep). A step that already has evidence is answered right here with
// the current state (alreadyRecorded) - a retry never stores anything new.
// step "" (the preview probe) skips that. ok is false once a response has
// been written.
func (s *Server) stepSession(w http.ResponseWriter, r *http.Request, step flow.Step) (session.Session, *flow.FlowDefinition, appCaller, bool) {
	sess, caller, ok := s.appSessionByPathToken(w, r)
	if !ok {
		return session.Session{}, nil, appCaller{}, false
	}
	resolvedFlow, err := s.resolveSessionFlow(r.Context(), sess)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not resolve flow: "+err.Error())
		return session.Session{}, nil, appCaller{}, false
	}
	if err := stepWriteCheck(sess, caller, step); err != nil {
		s.writeStepError(w, r, sess, resolvedFlow, caller, step, err)
		return session.Session{}, nil, appCaller{}, false
	}
	if resolvedFlow == nil {
		writeError(w, http.StatusConflict, "this session has no resolved flow definition; use POST .../result instead")
		return session.Session{}, nil, appCaller{}, false
	}
	return sess, resolvedFlow, caller, true
}

// errStepAlreadyRecorded: the step already has evidence, which is never
// replaced - the request is answered with the current state instead.
var errStepAlreadyRecorded = errors.New("step already recorded")

// stepWriteCheck is whether caller may write step's evidence to sess right
// now: it still holds its slot, the session hasn't expired or been
// cancelled, step has no evidence yet (errStepAlreadyRecorded) and the
// session has no outcome yet. Run on the request's snapshot and again
// inside the write's Update, where it is what actually counts.
func stepWriteCheck(sess session.Session, caller appCaller, step flow.Step) error {
	if caller.role != "" || sess.Access.Bound() {
		if err := sess.Access.AuthorizeAs(caller.deviceToken, caller.role); err != nil {
			return err
		}
	}
	switch sess.Status {
	case session.StatusExpired:
		return errSessionExpired
	case session.StatusCancelled:
		return errSessionComplete
	}
	if step != "" && stepHasEvidence(sess, step) {
		return errStepAlreadyRecorded
	}
	return sessionOpenForDevices(sess)
}

// writeStep stores one step's evidence: apply runs inside the Update only
// once stepWriteCheck passes there, so a device handed away from, a session
// that expired or finished, or a step recorded meanwhile - while this
// request was still processing - can't write. The device's participation
// records the step.
func (s *Server) writeStep(sess session.Session, caller appCaller, step flow.Step, apply func(*session.Session, time.Time)) (session.Session, error) {
	return s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		if err := stepWriteCheck(*sess, caller, step); err != nil {
			return err
		}
		now := time.Now().UTC()
		apply(sess, now)
		sess.Access.RecordStep(caller.role, string(step))
		return advanceToInProgress(sess, now)
	})
}

// writeStepError answers a refused step write: the current state for a
// step that was already recorded, the access/session error otherwise.
func (s *Server) writeStepError(w http.ResponseWriter, r *http.Request, sess session.Session, fd *flow.FlowDefinition, caller appCaller, step flow.Step, err error) {
	if !errors.Is(err, errStepAlreadyRecorded) {
		s.denyApp(w, r, sess, caller, err)
		return
	}
	if current, getErr := s.sessions.Get(sess.TenantID, sess.ID); getErr == nil {
		sess = current
	}
	details := actorDetails(sess, caller.role)
	details["stage"], details["route"] = string(step), r.Pattern
	s.auditProofing(sess, eventStepDuplicate, details)
	resp := s.stepResponseFor(sess, fd)
	resp.AlreadyRecorded = true
	writeJSON(w, http.StatusOK, resp)
}

// stepTiming stamps a step's StartedAt/SubmittedAt — see
// session.StepTiming's doc comment for why this is server time, not
// client-reported. existing is timing already recorded for this step (nil
// if none; today only the NFC submission superseding an MRZ-only
// document_capture passes one): its StartedAt is kept rather than reset, so
// it still reflects when the user first reached the step.
func stepTiming(existing *session.StepTiming, now time.Time) session.StepTiming {
	startedAt := now
	if existing != nil && !existing.StartedAt.IsZero() {
		startedAt = existing.StartedAt
	}
	return session.StepTiming{StartedAt: startedAt, SubmittedAt: now}
}

// advanceToInProgress walks sess from created (or opened) to in_progress —
// the status machine (session.transitions) has no direct created ->
// in_progress edge, so a session that reaches its first step submission
// without ever having called GET /api/v1/app/{token} (handleAppSession,
// which marks it opened) still needs both hops, not just one. A no-op for
// a session already at in_progress or later.
func advanceToInProgress(sess *session.Session, now time.Time) error {
	if sess.Status == session.StatusCreated {
		if err := sess.SetStatus(session.StatusOpened, now); err != nil {
			return err
		}
	}
	if sess.Status == session.StatusOpened {
		if err := sess.SetStatus(session.StatusInProgress, now); err != nil {
			return err
		}
	}
	return nil
}

// writeStepRejected answers a step request for a session that can no
// longer take one: 410 session_expired, or 409 session_complete once an
// outcome exists (terminal or needs_review).
func writeStepRejected(w http.ResponseWriter, sess session.Session) {
	if sess.Status == session.StatusExpired {
		writeErrorCode(w, http.StatusGone, errCodeSessionExpired, "session already expired; step rejected")
		return
	}
	msg := "session already finished; step rejected"
	if sess.Status == session.StatusNeedsReview {
		msg = "session already submitted; step rejected"
	}
	writeErrorCode(w, http.StatusConflict, errCodeSessionComplete, msg)
}

// stepResponse is what every step endpoint returns: the step's result is
// stored and the server has decided what comes next - CurrentStep is the
// step to continue with ("" once Lifecycle is COMPLETE), so the client
// never has to derive it.
type stepResponse struct {
	Status         session.Status `json:"status"`
	CompletedSteps []string       `json:"completedSteps"`
	CurrentStep    *string        `json:"currentStep,omitempty"`
	Lifecycle      string         `json:"lifecycle"`
	ErrorCode      string         `json:"errorCode,omitempty"`
	// ReadyToSubmit: every step has evidence and the session waits for the
	// user to submit it (POST .../submit) - see readyToSubmit.
	ReadyToSubmit bool `json:"readyToSubmit,omitempty"`
	// AlreadyRecorded is set when this request's step already had evidence
	// (or, for POST .../submit, the session was already submitted): nothing
	// was stored, the response is just the current state.
	AlreadyRecorded bool `json:"alreadyRecorded,omitempty"`
}

func (s *Server) stepResponseFor(sess session.Session, fd *flow.FlowDefinition) stepResponse {
	return stepResponse{
		Status: sess.Status, CompletedSteps: sess.CompletedSteps(), ErrorCode: sess.ErrorCode,
		CurrentStep: currentStep(sess, fd), Lifecycle: sessionLifecycle(sess), ReadyToSubmit: readyToSubmit(sess, fd),
	}
}

// readyToSubmit reports whether sess has evidence for every step fd
// requires but no outcome yet: it waits for POST .../submit.
func readyToSubmit(sess session.Session, fd *flow.FlowDefinition) bool {
	return fd != nil && sessionOpenForDevices(sess) == nil && requiredStepsComplete(sess, fd)
}

// ---- selfie / liveness / face_match -----------------------------------------

const (
	faceProviderRegula             = "regula"                    // SelfieStepEvidence.Provider, biometricsInfo.Engine
	errCodeFaceProviderUnavailable = "FACE_PROVIDER_UNAVAILABLE" // Regula unreachable or not configured
)

// submitSelfieStepRequest is the selfie/liveness step: the chosen frame,
// scored against the real anti-spoof model when one is loaded (see
// checkLiveness), plus a short burst of frames (Frames) still used for the
// frame-distinctness duplicate-submission check (api.injectionInfo) that
// check keeps enforcing alongside the model.
//
// LivenessTransactionID replaces Image when the app ran a Regula liveness
// session (appSessionView.FaceVerification): exactly one of the two is set.
type submitSelfieStepRequest struct {
	Image                 string   `json:"image,omitempty"`
	MimeType              string   `json:"mimeType,omitempty"`
	Frames                []string `json:"frames,omitempty"`
	LivenessTransactionID string   `json:"livenessTransactionId,omitempty"`
}

type selfieStepResponse struct {
	stepResponse
	Biometrics *biometricsInfo `json:"biometrics,omitempty"`
}

// handleSubmitSelfieStep is the selfie/liveness/face_match steps, computed
// together since they're one live capture. Because the browser flow always
// completes an nfc_read handover (when the resolved flow asks for one)
// before this step (see the frontend's screen order), the face_match
// reference — the chip's DG2 photo, or the relying party's own
// referencePhoto for a flow with face_match but no nfc_read — is always
// already resolved by the time this runs, so the match is computed here
// once, not deferred.
//
//	@Summary	Submit the selfie/liveness/face_match steps
//	@Tags		proofing-app
//	@Accept		json
//	@Produce	json
//	@Param		token	path		string							true	"Session token"
//	@Param		request	body		api.submitSelfieStepRequest	true	"Live selfie capture"
//	@Success	200		{object}	api.selfieStepResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	404		{object}	map[string]string
//	@Failure	409		{object}	map[string]string
//	@Failure	410		{object}	map[string]string
//	@Failure	503		{object}	map[string]string
//	@Router		/api/v1/app/{token}/steps/selfie [post]
func (s *Server) handleSubmitSelfieStep(w http.ResponseWriter, r *http.Request) {
	// The body is read before the session checks so a Regula transaction is
	// released on every path, including a refused request.
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
	var req submitSelfieStepRequest
	decodeErr := json.NewDecoder(r.Body).Decode(&req)
	if decodeErr == nil && req.LivenessTransactionID != "" && s.cfg.Regula != nil {
		defer regulaFaceVerifier{client: s.cfg.Regula}.Release(r.Context(), liveCapture{TransactionID: req.LivenessTransactionID})
	}
	sess, resolvedFlow, caller, ok := s.stepSession(w, r, flow.StepSelfie)
	if !ok {
		return
	}
	if decodeErr != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+decodeErr.Error())
		return
	}
	if (req.Image == "") == (req.LivenessTransactionID == "") {
		writeError(w, http.StatusBadRequest, "exactly one of image or livenessTransactionId is required")
		return
	}
	provider := s.faceProviderFor(resolvedFlow)
	verifier, ok := s.faceVerifier(provider)
	if !ok {
		writeFaceProviderUnavailable(w)
		return
	}
	live := liveCapture{TransactionID: req.LivenessTransactionID, Frames: req.Frames}
	var selfieMime string
	if req.Image != "" {
		raw, mime, err := decodeImageBase64(req.Image, firstNonEmpty(req.MimeType, "image/jpeg"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid image: "+err.Error())
			return
		}
		live.Image, selfieMime = raw, mime
	}

	var ref *faceReference
	if flowNeedsFaceMatch(resolvedFlow) {
		refImage, refMime, ok := s.faceMatchReference(sess)
		if !ok {
			writeError(w, http.StatusConflict, "the reference photo for face_match is not available yet")
			return
		}
		ref = &faceReference{ImageBase64: refImage, MimeType: refMime}
	}

	out, err := verifier.Verify(r.Context(), sess, ref, live)
	if err != nil {
		writeFaceVerifyError(w, provider, err)
		return
	}

	// Liveness and the face match above can take seconds; writeStep checks
	// again that this device still holds the session and nothing finished it
	// meanwhile, so a stale result is never stored.
	stage := faceStepName(resolvedFlow)
	updated, err := s.writeStep(sess, caller, flow.Step(stage), func(sess *session.Session, now time.Time) {
		ev := &session.SelfieStepEvidence{
			LivenessPassed: out.LivenessPassed, LivenessScore: out.LivenessScore,
			FaceMatchScore: out.MatchScore, FaceVerified: out.Matched,
			Timing: stepTiming(nil, now),
		}
		// Regula holds the capture itself and it is deleted with the
		// transaction: the selfie kept is the live face's crop from the match.
		if provider == flow.FaceProviderRegula {
			ev.Provider = faceProviderRegula
			ev.Image, ev.MimeType = out.Selfie, out.SelfieMime
		} else {
			ev.Image, ev.MimeType = req.Image, selfieMime
		}
		sess.Steps.Selfie = ev
	})
	if err != nil {
		s.writeStepError(w, r, sess, resolvedFlow, caller, flow.Step(stage), err)
		return
	}
	submitted := actorDetails(updated, caller.role)
	submitted["livenessPassed"] = out.LivenessPassed
	submitted["provider"] = string(provider)
	s.auditStepSubmitted(updated, resolvedFlow, stage, submitted)
	resp := selfieStepResponse{stepResponse: s.stepResponseFor(updated, resolvedFlow)}
	// Always populated: liveness runs on every selfie submission, with or
	// without a face match.
	resp.Biometrics = &biometricsInfo{
		FaceMatchScore: out.MatchScore, FaceVerified: out.Matched,
		LivenessResult: livenessResult(out.LivenessPassed), LivenessScore: out.LivenessScore,
		Engine: selfieEngine(updated.Steps.Selfie), Threshold: out.Threshold,
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeFaceVerifyError maps a FaceVerifier error to its HTTP response.
func writeFaceVerifyError(w http.ResponseWriter, provider flow.FaceProvider, err error) {
	var captureErr faceCaptureError
	switch {
	case errors.Is(err, errFaceProviderUnavailable):
		writeFaceProviderUnavailable(w)
	case errors.Is(err, errWrongFaceCapture):
		if provider == flow.FaceProviderRegula {
			writeError(w, http.StatusBadRequest, "this flow verifies faces with Regula: submit livenessTransactionId")
		} else {
			writeError(w, http.StatusBadRequest, "this flow verifies faces with the built-in engine: submit image")
		}
	case errors.Is(err, errUnknownTransaction), errors.Is(err, errForeignTransaction):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.As(err, &captureErr):
		writeError(w, http.StatusBadRequest, captureErr.Error())
	default:
		writeError(w, http.StatusInternalServerError, "face verification failed")
	}
}

// flowNeedsFaceMatch is whether the selfie step must also match against the
// reference photo (faceMatchReference).
func flowNeedsFaceMatch(fd *flow.FlowDefinition) bool {
	return slices.Contains(fd.Steps, flow.StepFaceVerification) || slices.Contains(fd.Steps, flow.StepFaceMatch)
}

// writeFaceProviderUnavailable fails a face step closed.
func writeFaceProviderUnavailable(w http.ResponseWriter) {
	writeErrorCode(w, http.StatusServiceUnavailable, errCodeFaceProviderUnavailable, errCodeFaceProviderUnavailable)
}

// livenessResult is biometricsInfo.LivenessResult for a check that ran.
func livenessResult(passed bool) string {
	if passed {
		return "passed"
	}
	return "failed"
}

// faceMatchReference is what a selfie is compared against: the relying
// party's reference photo (a flow without nfc_read, required at creation), or
// the chip's DG2 photo from the nfc_read step. ok is false while that has not
// landed; the app reads the chip first, but the server never trusts client
// sequencing.
func (s *Server) faceMatchReference(sess session.Session) (image, mimeType string, ok bool) {
	if sess.ReferencePhoto != "" {
		return sess.ReferencePhoto, sess.ReferencePhotoMime, true
	}
	if sess.Steps.NFC != nil {
		if req, err := decodeNFCStepRequest(sess.Steps.NFC.Raw); err == nil && req.Photo != nil {
			return req.Photo.ImageBase64, req.Photo.MimeType, true
		}
	}
	return "", "", false
}

// ---- nfc_read ---------------------------------------------------------------

// submitNFCStepRequest is what vcmrtd posts for the nfc_read step: the same
// shape appResultRequest always carried for this part (Photo/MrtdEvidence/
// Document/Device), just through its own endpoint instead of the old
// single-shot POST .../result — see this file's package doc comment for
// why.
//
// Document, when the resolved flow's Steps includes document_capture (see
// flow.Validate — it's always paired with nfc_read), is the *entire*
// document identity vcmrtd read off the document itself (the same full
// shape appResultRequest.Document always carried) — there is no other
// source of it at all, since document_capture has no browser-side
// counterpart. handleSubmitNFCStep derives session.DocumentStepEvidence
// straight from it (see documentEvidenceFromNativeDocument).
type submitNFCStepRequest struct {
	Photo        *photoInfo           `json:"photo,omitempty"`
	Document     *documentInfo        `json:"document,omitempty"`
	MrtdEvidence *mrtdEvidenceRequest `json:"mrtdEvidence"`
	Device       *deviceInfo          `json:"device,omitempty"`
}

// handleSubmitNFCStep is the nfc_read step.
//
//	@Summary	Submit the nfc_read step
//	@Tags		proofing-app
//	@Accept		json
//	@Produce	json
//	@Param		token	path		string						true	"Session token"
//	@Param		request	body		api.submitNFCStepRequest	true	"Chip evidence"
//	@Success	200		{object}	api.stepResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	404		{object}	map[string]string
//	@Failure	409		{object}	map[string]string
//	@Failure	410		{object}	map[string]string
//	@Router		/api/v1/app/{token}/steps/nfc [post]
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
	if _, err := verifyMrtdEvidence(req.MrtdEvidence, sess.AAChallenge); err != nil {
		writeError(w, http.StatusBadRequest, "invalid mrtdEvidence: "+err.Error())
		return
	}
	// The photo is the face_match reference (faceMatchReference), so it must
	// be the chip's own portrait: see photoFromChip.
	if req.Photo != nil && !photoFromChip(req.Photo, req.MrtdEvidence) {
		writeError(w, http.StatusBadRequest, "photo is not the portrait in mrtdEvidence's DG2")
		return
	}
	// The chip can carry a BSN even on documents whose MRZ/VIZ never did
	// (older Dutch documents still populate DG11's personal number). Apply
	// the tenant's BSN policy here, before req.Document is ever persisted
	// into Steps.NFC.Raw/Steps.Document.Parsed below - the same guarantee
	// buildResult gives Session.Result (see applyBSNPolicy's doc comment)
	// must also hold for Session.Steps, which is stored independently and
	// never re-redacted afterward. That guarantee covers req.Document's
	// parsed PersonalNumber field; req.MrtdEvidence.DataGroups["DG11"] is
	// the same BSN again, but as the chip's own raw bytes, and marshalToMap
	// below persists req (MrtdEvidence included) verbatim - so
	// redactBSNFromEvidence has to redact that copy too, or it would reach
	// Steps.NFC.Raw unmasked regardless of policy.
	// Without a document in this request, the issuing state (which decides
	// whether DG11 can carry a BSN) comes from an already-recorded MRZ
	// document step; with neither, redactBSNFromEvidence can't tell and
	// leaves DG11 alone, so drop it under a mask/omit policy rather than
	// store a possibly-unmasked BSN.
	bsnPolicy, _ := effectivePrivacyPolicy(resolvedFlow)
	switch {
	case req.Document != nil:
		applyBSNPolicy(req.Document, bsnPolicy)
		redactBSNFromEvidence(req.MrtdEvidence, req.Document, bsnPolicy)
	case sess.Steps.Document != nil && sess.Steps.Document.Parsed.IssuingState != "":
		redactBSNFromEvidence(req.MrtdEvidence, &documentInfo{IssuingState: sess.Steps.Document.Parsed.IssuingState}, bsnPolicy)
	default:
		if p := bsnPolicy.Effective(); p == privacy.BSNPolicyMask || p == privacy.BSNPolicyOmit {
			delete(req.MrtdEvidence.DataGroups, "DG11")
		}
	}
	raw, err := marshalToMap(req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not store nfc evidence: "+err.Error())
		return
	}

	hasDocumentCapture := slices.Contains(resolvedFlow.Steps, flow.StepDocumentCapture)
	updated, err := s.writeStep(sess, caller, flow.StepNFCRead, func(sess *session.Session, now time.Time) {
		sess.Steps.NFC = &session.NFCStepEvidence{Raw: raw, Timing: stepTiming(nil, now)}
		if !hasDocumentCapture {
			return
		}
		// document_capture, if vcmrtd didn't already submit it on its own
		// (POST .../steps/document_capture), is fulfilled by this same
		// submission. A document sent here supersedes the MRZ-only one: it
		// arrives together with the chip evidence that vouches for it. The
		// MRZ copy's chip access key isn't needed any more either way.
		switch {
		case req.Document != nil || sess.Steps.Document == nil:
			var existing *session.StepTiming
			if sess.Steps.Document != nil {
				existing = &sess.Steps.Document.Timing
			} else {
				sess.Access.RecordStep(caller.role, string(flow.StepDocumentCapture))
			}
			evidence := documentEvidenceFromNativeDocument(req.Document)
			evidence.Timing = stepTiming(existing, now)
			evidence.Source = session.DocumentSourceChip
			sess.Steps.Document = &evidence
		default:
			// Copy rather than write through the pointer: the in-memory
			// store's Update works on a shallow copy, so the old evidence
			// may still be read elsewhere (and must survive a failed flush).
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

// photoFromChip reports whether photo's image bytes are embedded verbatim in
// ev's DG2, the holder's portrait (ICAO 9303), as vcmrtd sends the image it
// extracted from it. Passive Authentication hashes DG2 against EF.SOD, but
// not the separately submitted photo: without this, a genuine chip read could
// be paired with any face, and a face match against it would bind the
// subject to that face instead of to the document.
func photoFromChip(photo *photoInfo, ev *mrtdEvidenceRequest) bool {
	raw, _, err := decodeImageBase64(photo.ImageBase64, photo.MimeType)
	if err != nil || len(raw) == 0 {
		return false
	}
	dg2, err := hex.DecodeString(ev.DataGroups["DG2"])
	return err == nil && bytes.Contains(dg2, raw)
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

// ---- completion --------------------------------------------------------------

// requiredStepsComplete reports whether sess has accumulated evidence for
// every step fd.Steps lists.
func requiredStepsComplete(sess session.Session, fd *flow.FlowDefinition) bool {
	for _, step := range fd.Steps {
		if !stepHasEvidence(sess, step) {
			return false
		}
	}
	return true
}

// stepHasEvidence reports whether sess already holds the evidence that
// fulfils step - the selfie submission covers every face step at once.
func stepHasEvidence(sess session.Session, step flow.Step) bool {
	switch step {
	case flow.StepDocumentCapture:
		return sess.Steps.Document != nil
	case flow.StepNFCRead:
		return sess.Steps.NFC != nil
	case flow.StepDocumentPhoto:
		return sess.Steps.DocumentPhoto != nil
	case flow.StepSelfie, flow.StepLiveness, flow.StepFaceMatch, flow.StepFaceVerification:
		return sess.Steps.Selfie != nil
	}
	return true
}

// faceStepName is the name fd itself uses for its face step (e.g.
// face_verification, or selfie), so the audit log names the step the way the
// flow definition does. The selfie submission fulfils all of them at once.
func faceStepName(fd *flow.FlowDefinition) string {
	for _, step := range fd.Steps {
		switch step {
		case flow.StepFaceVerification, flow.StepSelfie, flow.StepLiveness, flow.StepFaceMatch:
			return string(step)
		}
	}
	return string(flow.StepSelfie)
}

// errStepAlreadyStarted makes markStepStarted's Update a no-op once another
// request recorded the same start first.
var errStepAlreadyStarted = errors.New("step already started")

// markStepStarted records that the user began step (without evidence yet),
// moving the session to in_progress and appending an eventSessionInProgress
// with stepState "started" - once per step; every later call for the same
// step, or one for a step that already has evidence, is a cheap no-op, so
// it's safe from a per-frame endpoint like the selfie preview.
func (s *Server) markStepStarted(sess session.Session, fd *flow.FlowDefinition, step string, caller appCaller) error {
	if slices.Contains(sess.Steps.Started, step) || stepHasEvidence(sess, flow.Step(step)) {
		return nil
	}
	updated, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		if err := checkAppWrite(sess, caller); err != nil {
			return err
		}
		if slices.Contains(sess.Steps.Started, step) || stepHasEvidence(*sess, flow.Step(step)) {
			return errStepAlreadyStarted
		}
		sess.Steps.Started = append(sess.Steps.Started, step)
		return advanceToInProgress(sess, time.Now().UTC())
	})
	if errors.Is(err, errStepAlreadyStarted) {
		return nil
	}
	if err != nil {
		return err
	}
	s.auditStepStarted(updated, fd, step, actorDetails(updated, caller.role))
	return nil
}

// handleStartStep is the per-step "the user just began this step" signal,
// sent by whoever runs the step the moment it starts - vcmrtd when it opens
// its MRZ camera (document_capture) and when it starts talking to the chip
// (nfc_read), the browser when it turns the camera on for the face step. It
// moves the session to in_progress and appends one
// proofing.session.in_progress audit event naming the step (see
// markStepStarted), so the audit log shows each step as it starts rather
// than only once its evidence is submitted - that submission is recorded
// separately as proofing.result.submitted. Idempotent: repeating it for a
// step that already started, or already has evidence, changes nothing.
//
// Unlike the evidence endpoints this works without a resolved flow too, so a
// session still on the single-shot POST .../result gets the same per-step
// trail; with a flow, the step must be one the flow actually contains.
//
//	@Summary	Mark a step as started
//	@Tags		proofing-app
//	@Produce	json
//	@Param		token	path		string	true	"Session token"
//	@Param		step	path		string	true	"document_capture, nfc_read, or the flow's face step (face_verification, selfie, liveness, face_match)"
//	@Success	200		{object}	api.stepResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	401		{object}	map[string]string
//	@Failure	403		{object}	map[string]string
//	@Failure	404		{object}	map[string]string
//	@Failure	409		{object}	map[string]string
//	@Failure	410		{object}	map[string]string
//	@Router		/api/v1/app/{token}/steps/{step}/start [post]
func (s *Server) handleStartStep(w http.ResponseWriter, r *http.Request) {
	sess, resolvedFlow, step, caller, ok := s.appStepRequest(w, r)
	if !ok {
		return
	}
	if err := sessionOpenForDevices(sess); err != nil {
		details := actorDetails(sess, caller.role)
		details["reason"], details["route"] = accessErrorCode(err), r.Pattern
		s.auditProofing(sess, eventAccessDenied, details)
		writeStepRejected(w, sess)
		return
	}
	if err := s.markStepStarted(sess, resolvedFlow, string(step), caller); err != nil {
		s.denyApp(w, r, sess, caller, err)
		return
	}
	current, err := s.sessions.Get(sess.TenantID, sess.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not reload session: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.stepResponseFor(current, resolvedFlow))
}

// appStepRequest is the shared front half of the /steps/{step}/... routes:
// the authorized session, its flow (nil without one) and the {step} path
// value, normalised - any face step name maps to the one the flow itself
// uses, since a single capture fulfils them all - and checked against the
// flow.
func (s *Server) appStepRequest(w http.ResponseWriter, r *http.Request) (session.Session, *flow.FlowDefinition, flow.Step, appCaller, bool) {
	sess, caller, ok := s.appSessionByPathToken(w, r)
	if !ok {
		return session.Session{}, nil, "", appCaller{}, false
	}
	step := flow.Step(r.PathValue("step"))
	if !flow.ValidStep(step) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown step %q", step))
		return session.Session{}, nil, "", appCaller{}, false
	}
	resolvedFlow, err := s.resolveSessionFlow(r.Context(), sess)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not resolve flow: "+err.Error())
		return session.Session{}, nil, "", appCaller{}, false
	}
	if resolvedFlow != nil {
		if isFaceStep(step) {
			step = flow.Step(faceStepName(resolvedFlow))
		}
		if !slices.Contains(resolvedFlow.Steps, step) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("step %q is not part of this session's flow", step))
			return session.Session{}, nil, "", appCaller{}, false
		}
	}
	return sess, resolvedFlow, step, caller, true
}

// stepResultResponse is GET .../steps/{step}/result: one step's stored
// result plus where the session stands now.
type stepResultResponse struct {
	stepResultView
	stepResponse
}

// handleStepResult reads back one step's server-side result - what a
// reloaded or handed-over client uses to confirm a step it (or the other
// application) completed really landed, before moving on to CurrentStep.
// Verdicts only (liveness passed, document checks valid, ...), never the
// evidence itself. Readable after completion too: the result doesn't go
// away when the session finishes.
//
//	@Summary	Get one step's stored result
//	@Tags		proofing-app
//	@Produce	json
//	@Param		token	path		string	true	"Session token"
//	@Param		step	path		string	true	"Step name"
//	@Success	200		{object}	api.stepResultResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	401		{object}	map[string]string
//	@Failure	403		{object}	map[string]string
//	@Failure	404		{object}	map[string]string
//	@Router		/api/v1/app/{token}/steps/{step}/result [get]
func (s *Server) handleStepResult(w http.ResponseWriter, r *http.Request) {
	sess, resolvedFlow, step, _, ok := s.appStepRequest(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, stepResultResponse{
		stepResultView: stepResultFor(sess, step),
		stepResponse:   s.stepResponseFor(sess, resolvedFlow),
	})
}

// submitDocumentStepRequest is the document_capture step on its own: the
// document identity vcmrtd read from the MRZ (the same documentInfo shape
// POST .../steps/nfc carries), sent the moment the MRZ scan completes.
type submitDocumentStepRequest struct {
	Document *documentInfo `json:"document"`
	// ChipAccess is the MRZ-derived key to open the chip, kept until
	// nfc_read lands so a device taking over can go straight to the chip
	// read (appSessionView.ChipAccess). Optional.
	ChipAccess *session.ChipAccessKey `json:"chipAccess,omitempty"`
}

// handleSubmitDocumentStep stores the document_capture step's result as
// soon as vcmrtd has read the MRZ, instead of only alongside the chip read,
// so every step reaches the server when it completes and the server moves
// the session on to nfc_read. The later POST .../steps/nfc may still carry
// the document too; that chip-backed copy then supersedes this one.
//
//	@Summary	Submit the document_capture step
//	@Tags		proofing-app
//	@Accept		json
//	@Produce	json
//	@Param		token	path		string							true	"Session token"
//	@Param		request	body		api.submitDocumentStepRequest	true	"Document identity from the MRZ"
//	@Success	200		{object}	api.stepResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	401		{object}	map[string]string
//	@Failure	403		{object}	map[string]string
//	@Failure	404		{object}	map[string]string
//	@Failure	409		{object}	map[string]string
//	@Failure	410		{object}	map[string]string
//	@Router		/api/v1/app/{token}/steps/document_capture [post]
func (s *Server) handleSubmitDocumentStep(w http.ResponseWriter, r *http.Request) {
	sess, resolvedFlow, caller, ok := s.stepSession(w, r, flow.StepDocumentCapture)
	if !ok {
		return
	}
	if !slices.Contains(resolvedFlow.Steps, flow.StepDocumentCapture) {
		writeError(w, http.StatusBadRequest, "step \"document_capture\" is not part of this session's flow")
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
	// Same BSN guarantee as handleSubmitNFCStep: the policy applies before
	// anything is persisted into Steps.
	bsnPolicy, _ := effectivePrivacyPolicy(resolvedFlow)
	applyBSNPolicy(req.Document, bsnPolicy)
	keepChipAccess := slices.Contains(resolvedFlow.Steps, flow.StepNFCRead)

	updated, err := s.writeStep(sess, caller, flow.StepDocumentCapture, func(sess *session.Session, now time.Time) {
		evidence := documentEvidenceFromNativeDocument(req.Document)
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

// submitDocumentPhotoStepRequest is the document_photo step: the photo of
// the document's printed data page or card, and where on it the printed BSN
// is, when the app found it.
type submitDocumentPhotoStepRequest struct {
	Front *documentPhotoSideRequest `json:"front"`
	// Back is omitted for a document without one worth taking (a passport).
	Back *documentPhotoSideRequest `json:"back,omitempty"`
}

// documentPhotoSideRequest is one side's photo, and where on it the printed
// BSN is, when the app found it.
type documentPhotoSideRequest struct {
	Image     string       `json:"image"`
	MimeType  string       `json:"mimeType,omitempty"`
	BSNRegion *imageRegion `json:"bsnRegion,omitempty"`
}

// handleSubmitDocumentPhotoStep stores the document_photo step's photos of
// the document's front and (optionally) back. They are released as the
// result's documentImage and documentImageBack (attrDocumentImage), each BSN
// region blurred under a BlurBSN policy (buildResult), and the stored copies
// blurred the same way once the result is built (redactStepsForStorage).
//
//	@Summary	Submit the document_photo step
//	@Tags		proofing-app
//	@Accept		json
//	@Produce	json
//	@Param		token	path		string								true	"Session token"
//	@Param		request	body		api.submitDocumentPhotoStepRequest	true	"Photo of the document"
//	@Success	200		{object}	api.stepResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	401		{object}	map[string]string
//	@Failure	403		{object}	map[string]string
//	@Failure	404		{object}	map[string]string
//	@Failure	409		{object}	map[string]string
//	@Failure	410		{object}	map[string]string
//	@Router		/api/v1/app/{token}/steps/document_photo [post]
func (s *Server) handleSubmitDocumentPhotoStep(w http.ResponseWriter, r *http.Request) {
	sess, resolvedFlow, caller, ok := s.stepSession(w, r, flow.StepDocumentPhoto)
	if !ok {
		return
	}
	if !slices.Contains(resolvedFlow.Steps, flow.StepDocumentPhoto) {
		writeError(w, http.StatusBadRequest, "step \"document_photo\" is not part of this session's flow")
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

// documentPhotoSide validates one side's photo (name says which, for the
// error) and returns it as stored evidence.
func documentPhotoSide(name string, in documentPhotoSideRequest) (session.DocumentPhotoSide, error) {
	if in.Image == "" {
		return session.DocumentPhotoSide{}, fmt.Errorf("%s.image is required", name)
	}
	raw, mime, err := decodeImageBase64(in.Image, firstNonEmpty(in.MimeType, "image/jpeg"))
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

func validChipAccessDocumentType(t string) bool {
	return t == "passport" || t == "identity_card" || t == "drivers_license"
}

// isFaceStep reports whether step is one of the face steps the single
// selfie capture fulfils.
func isFaceStep(step flow.Step) bool {
	switch step {
	case flow.StepSelfie, flow.StepLiveness, flow.StepFaceMatch, flow.StepFaceVerification:
		return true
	}
	return false
}

// flowStepProgress splits fd's steps, in flow order, into those sess
// already has evidence for and those still to come.
func flowStepProgress(sess session.Session, fd *flow.FlowDefinition) (completed, remaining []string) {
	completed, remaining = []string{}, []string{}
	for _, step := range fd.Steps {
		if stepHasEvidence(sess, step) {
			completed = append(completed, string(step))
		} else {
			remaining = append(remaining, string(step))
		}
	}
	return completed, remaining
}

// errStepsIncomplete: POST .../submit before every step has evidence - or
// the session's steps were reset while its result was being built.
var errStepsIncomplete = errors.New("not every step of this session has a result yet")

// handleSubmitSession is the user submitting a session whose steps are all
// done (readyToSubmit): only now does it get its outcome (finishSession).
// Any device holding one of the session's slots may submit. Submitting a
// session that already has its outcome (a double tap, a retry) is answered
// with the current state (alreadyRecorded) and changes nothing.
//
//	@Summary	Submit a session whose steps are all done
//	@Tags		proofing-app
//	@Produce	json
//	@Param		token	path		string	true	"Session token"
//	@Success	200		{object}	api.stepResponse
//	@Failure	401		{object}	map[string]string
//	@Failure	403		{object}	map[string]string
//	@Failure	404		{object}	map[string]string
//	@Failure	409		{object}	map[string]string
//	@Failure	410		{object}	map[string]string
//	@Router		/api/v1/app/{token}/submit [post]
func (s *Server) handleSubmitSession(w http.ResponseWriter, r *http.Request) {
	sess, caller, ok := s.appSessionByPathToken(w, r)
	if !ok {
		return
	}
	resolvedFlow, err := s.resolveSessionFlow(r.Context(), sess)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not resolve flow: "+err.Error())
		return
	}
	if resolvedFlow == nil {
		writeError(w, http.StatusConflict, "this session has no resolved flow definition; use POST .../result instead")
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
	case errors.Is(err, errStepsIncomplete):
		writeErrorCode(w, http.StatusConflict, errCodeStepsIncomplete, err.Error())
	case accessErrorCode(err) != "":
		s.denyApp(w, r, sess, caller, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, "could not finish session: "+err.Error())
	default:
		writeJSON(w, http.StatusOK, s.stepResponseFor(updated, resolvedFlow))
	}
}

// finishSession gives sess, whose every step fd requires has evidence, its
// outcome — the server, not the caller, decides it. It assembles the same
// appResultRequest shape handleAppSessionResult always built from one
// request body, just sourced from sess.Steps' accumulated evidence, then
// runs the exact same authenticityFailure/flowComplianceFailure/buildResult
// chain. The write re-checks caller's slot and the session's state
// (checkAppWrite) and that every step still has evidence (errStepsIncomplete).
func (s *Server) finishSession(ctx context.Context, sess session.Session, fd *flow.FlowDefinition, caller appCaller) (session.Session, error) {
	if err := checkAppWrite(&sess, caller); err != nil {
		return sess, err
	}
	if !requiredStepsComplete(sess, fd) {
		return sess, errStepsIncomplete
	}

	req := appResultRequest{Status: session.StatusApproved}
	var verifiedChecks *chipChecksInfo
	if p := sess.Steps.DocumentPhoto; p != nil {
		req.DocumentImage = documentImageFromSide(&p.Front)
		req.DocumentImageBack = documentImageFromSide(p.Back)
	}
	if sess.Steps.Document != nil {
		doc := documentInfoFromParsedMRZ(sess.Steps.Document.Parsed)
		req.Document = &doc
	}
	if sess.Steps.Selfie != nil {
		// A Regula selfie without a crop (no match ran, or Regula found no
		// face) keeps no image: the result then has no "selfie".
		if sess.Steps.Selfie.Image != "" {
			if raw, mime, err := decodeImageBase64(sess.Steps.Selfie.Image, firstNonEmpty(sess.Steps.Selfie.MimeType, "image/jpeg")); err == nil {
				req.Selfie = &photoInfo{ImageBase64: base64.StdEncoding.EncodeToString(raw), MimeType: mime}
			}
		}
		// LivenessPassed is computed unconditionally on every selfie
		// submission (handleSubmitSelfieStep), regardless of whether the
		// flow even asks for face_match — so once Steps.Selfie is non-nil
		// the check has always actually run; "not_performed" would
		// misreport a check that ran and failed as one that never ran at
		// all (requirements.md §5: "unambiguous statuses"). Matches
		// handleSubmitSelfieStep's own immediate response exactly.
		req.Biometrics = &biometricsInfo{
			FaceMatchScore: sess.Steps.Selfie.FaceMatchScore, FaceVerified: sess.Steps.Selfie.FaceVerified,
			LivenessResult: livenessResult(sess.Steps.Selfie.LivenessPassed), LivenessScore: sess.Steps.Selfie.LivenessScore,
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
		checks, err := verifyMrtdEvidence(req.MrtdEvidence, sess.AAChallenge)
		if err != nil {
			return sess, err
		}
		verifiedChecks = checks
		if nfcReq.Document != nil && req.Document != nil {
			// DG11 extras only the chip carries — prefer them the same way
			// documentInfo.DisplayName's own doc comment already prefers
			// DG11 over MRZ.
			req.Document.PersonalNumber = firstNonEmpty(nfcReq.Document.PersonalNumber, req.Document.PersonalNumber)
			req.Document.PlaceOfBirth = nfcReq.Document.PlaceOfBirth
			if nfcReq.Document.DisplayName != "" {
				req.Document.DisplayName = nfcReq.Document.DisplayName
			}
		}
	}

	bsnPolicy, redactionPolicy := effectivePrivacyPolicy(fd)
	result := buildResult(sess, req, verifiedChecks, fd, bsnPolicy, redactionPolicy)

	finalStatus, finalErrorCode, authenticityOverridden := session.StatusApproved, "", false
	if failed, code := authenticityFailure(verifiedChecks); failed {
		finalStatus, finalErrorCode, authenticityOverridden = session.StatusRejected, code, true
	}
	flowViolated := false
	if !authenticityOverridden {
		if failed, code := flowComplianceFailure(fd, sess, req); failed {
			finalStatus, finalErrorCode, flowViolated = session.StatusRejected, code, true
		}
	}

	updated, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		if err := checkAppWrite(sess, caller); err != nil {
			return err
		}
		// result was built from the snapshot above; if the session was reset
		// (POST .../reset) in between, that evidence is gone and must not be
		// turned into an outcome.
		if !requiredStepsComplete(*sess, fd) {
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
		// Result has just been built from sess.Steps above (buildResult, via
		// req) - nothing after this point ever needs the raw evidence again,
		// so this is the first point it's safe to redact the copy Steps
		// itself retains. See redactStepsForStorage's doc comment for why it
		// can't run any earlier (it would corrupt the face_match reference).
		redactStepsForStorage(sess, redactionPolicy)
		return nil
	})
	if err != nil {
		return sess, err
	}
	submitted := actorDetails(updated, caller.role)
	submitted["stage"] = "session_submit"
	s.auditProofing(updated, eventResultSubmitted, submitted)
	outcomeDetails := map[string]any{}
	if updated.ErrorCode != "" {
		outcomeDetails["errorCode"] = updated.ErrorCode
	}
	if authenticityOverridden {
		outcomeDetails["reason"] = "tamper_detected"
	}
	if flowViolated {
		outcomeDetails["reason"] = "flow_policy_violation"
	}
	if devices := deviceParticipation(updated.Access); len(devices) > 0 {
		outcomeDetails["devices"] = devices
	}
	if updated.Status == session.StatusApproved {
		if photo, ok := result["photo"]; ok {
			outcomeDetails["photo"] = photo
		}
		if selfie, ok := result["selfie"]; ok {
			outcomeDetails["selfie"] = selfie
		}
	}
	s.auditProofing(updated, eventTypeForStatus(updated.Status), outcomeDetails)
	return updated, nil
}

// redactStepsForStorage applies the tenant's BlurFace redaction policy to
// sess.Steps in place - closing docs/compliance.md §6.1.1's gap, where
// Session.Steps kept the full-resolution selfie image and the chip's raw
// DG2 photo forever, regardless of RedactionPolicy (only Session.Result,
// built from the same evidence, was ever redacted).
//
// It must only be called once every read of the raw image evidence has
// already happened - specifically after buildResult has run (finishSession
// calls this from within the same Update closure that just set sess.Result
// from it). Redacting any earlier would corrupt real evidence still in use:
// faceMatchReference reads Steps.NFC.Raw's photo as the face_match
// reference image for computeFaceMatch, and that happens at the *next*
// step's submission time, not this one's - blurring it up front would make
// every subsequent face match fail against pixelated noise instead of the
// actual chip photo. BSN masking has no equivalent ordering problem
// (handleSubmitNFCStep already applies it before Steps.NFC.Raw/
// Steps.Document.Parsed are ever persisted, see its own comment), so this
// only ever touches face imagery.
func redactStepsForStorage(sess *session.Session, redaction privacy.RedactionPolicy) {
	if redaction.BlurBSN {
		redactStoredDocumentPhoto(sess)
	}
	if !redaction.BlurFace {
		return
	}
	if sess.Steps.Selfie != nil && sess.Steps.Selfie.Image != "" {
		// A copy, not a write through the pointer - see the NFC step's
		// ChipAccess comment in handleSubmitNFCStep.
		selfie := *sess.Steps.Selfie
		if blurred, mime, ok := blurFace(selfie.Image, selfie.MimeType); ok {
			selfie.Image, selfie.MimeType = blurred, mime
		} else {
			slog.Warn("identity proofing: could not blur the stored selfie; dropping it", slog.String("session_id", sess.ID))
			selfie.Image, selfie.MimeType = "", ""
		}
		sess.Steps.Selfie = &selfie
	}
	if sess.Steps.NFC == nil {
		return
	}
	req, err := decodeNFCStepRequest(sess.Steps.NFC.Raw)
	if err != nil || req.Photo == nil || req.Photo.ImageBase64 == "" {
		return
	}
	if blurred, mime, ok := blurFace(req.Photo.ImageBase64, req.Photo.MimeType); ok {
		req.Photo.ImageBase64, req.Photo.MimeType = blurred, mime
	} else {
		slog.Warn("identity proofing: could not blur the stored chip photo; dropping it", slog.String("session_id", sess.ID))
		req.Photo = nil
	}
	raw, err := marshalToMap(req)
	if err != nil {
		// Never keep the unblurred original: without a re-marshalled copy,
		// drop the stored chip evidence's raw form altogether.
		slog.Warn("identity proofing: could not store the blurred chip photo", slog.String("session_id", sess.ID), slog.Any("error", err))
		raw = nil
	}
	nfc := *sess.Steps.NFC
	nfc.Raw = raw
	sess.Steps.NFC = &nfc
}

// documentImageFromSide is a stored side as the result request carries it;
// nil for no side.
func documentImageFromSide(side *session.DocumentPhotoSide) *documentImageInfo {
	if side == nil {
		return nil
	}
	img := &documentImageInfo{ImageBase64: side.Image, MimeType: side.MimeType}
	if r := side.BSNRegion; r != nil {
		img.BSNRegion = &imageRegion{X: r.X, Y: r.Y, W: r.W, H: r.H}
	}
	return img
}

// redactStoredDocumentPhoto blurs the printed BSN in the stored document
// photos, as buildResult already did in the result. A photo whose BSN could
// not be blurred is dropped rather than kept readable.
func redactStoredDocumentPhoto(sess *session.Session) {
	p := sess.Steps.DocumentPhoto
	if p == nil {
		return
	}
	photo := *p
	photo.Front = redactedSide(sess, photo.Front)
	if photo.Back != nil {
		back := redactedSide(sess, *photo.Back)
		photo.Back = &back
	}
	sess.Steps.DocumentPhoto = &photo
}

func redactedSide(sess *session.Session, side session.DocumentPhotoSide) session.DocumentPhotoSide {
	if side.BSNRegion == nil || side.Image == "" {
		return side
	}
	r := redact.Rect{X: side.BSNRegion.X, Y: side.BSNRegion.Y, W: side.BSNRegion.W, H: side.BSNRegion.H}
	if blurred, mime, err := redact.Region(side.Image, side.MimeType, r); err != nil {
		slog.Warn("identity proofing: could not blur the BSN in a document photo; dropping it", slog.String("session_id", sess.ID), slog.Any("error", err))
		side.Image, side.MimeType = "", ""
	} else {
		side.Image, side.MimeType = blurred, mime
	}
	return side
}

// blurFace pixelates a face image under a BlurFace redaction policy. It
// takes what the API accepts - plain or data-URL base64, and a chip's
// JPEG2000 DG2 portrait, which redact.Image can't decode on its own - and
// fails closed: ok is false when the image couldn't be blurred, and callers
// must then drop it rather than keep the unblurred original.
func blurFace(imageBase64, mimeType string) (string, string, bool) {
	raw, mime, err := decodeImageBase64(imageBase64, mimeType)
	if err != nil {
		return "", "", false
	}
	plain := base64.StdEncoding.EncodeToString(raw)
	if converted, convertedMime, err := images.ToDisplayablePNG(plain, mime); err == nil {
		plain, mime = converted, convertedMime
	}
	blurred, blurredMime, err := redact.Image(plain, mime)
	if err != nil {
		return "", "", false
	}
	return blurred, blurredMime, true
}

// documentEvidenceFromNativeDocument builds document_capture's evidence
// from vcmrtd's own submitted document identity — see submitNFCStepRequest's
// doc comment; this is document_capture's only source, always. No VIZ
// image: there is no document photo anywhere in this flow, so a session's
// result never has a documentImage from document_capture — an expected
// trade-off, not a bug.
func documentEvidenceFromNativeDocument(doc *documentInfo) session.DocumentStepEvidence {
	if doc == nil {
		return session.DocumentStepEvidence{}
	}
	return session.DocumentStepEvidence{
		Parsed: session.ParsedMRZ{
			DocumentType: doc.Type, IssuingState: doc.IssuingState, Number: doc.Number,
			Nationality: doc.Nationality, Surname: doc.LastName, GivenNames: doc.FirstName,
			Sex: doc.Sex, DateOfBirth: doc.DateOfBirth, DateOfExpiry: doc.DateOfExpiry,
			PersonalNumber: doc.PersonalNumber,
			// AllChecksValid: there's no MRZ/OCR checksum to validate here at
			// all - the chip's own Passive Authentication (verifyMrtdEvidence,
			// already run before this is called) is what actually vouches for
			// this data instead. True here means "nothing failed", not
			// "checksums ran and passed".
			AllChecksValid: true,
		},
	}
}

func documentInfoFromParsedMRZ(p session.ParsedMRZ) documentInfo {
	valid := p.AllChecksValid
	notExpired := isFutureDate(p.DateOfExpiry)
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

func isFutureDate(yyyyMMdd string) bool {
	t, err := time.Parse("2006-01-02", yyyyMMdd)
	if err != nil {
		return false
	}
	return t.After(time.Now().UTC())
}
