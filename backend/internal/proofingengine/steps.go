// The Idem app's step endpoints (/api/v1/app/{token}/steps/...). A session
// collects its evidence one step at a time, so a device taking it over resumes
// where the last stopped. A step never finishes the session: once every step
// has evidence the session is readyToSubmit, and POST .../submit lets the
// server decide the outcome (finishSession). document_capture (the MRZ the app
// scanned) and nfc_read (the chip) always come together.
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

// stepSession resolves the path token to a session whose flow can still take
// step, checked on this snapshot so a doomed request fails early (the write
// checks again). A step that already has evidence is answered with the current
// state. step "" (the preview probe) skips that. ok is false once a response
// has been written.
func (s *Server) stepSession(w http.ResponseWriter, r *http.Request, step flow.Step) (session.Session, *flow.FlowDefinition, appCaller, bool) {
	sess, caller, ok := s.appSessionByPathToken(w, r)
	if !ok {
		return session.Session{}, nil, appCaller{}, false
	}
	resolvedFlow, err := s.resolveSessionFlow(r.Context(), sess)
	if err != nil {
		writeInternalError(w, r, "could not resolve flow", err)
		return session.Session{}, nil, appCaller{}, false
	}
	if err := stepWriteCheck(sess, caller, step); err != nil {
		s.writeStepError(w, r, sess, resolvedFlow, caller, step, err)
		return session.Session{}, nil, appCaller{}, false
	}
	if resolvedFlow == nil {
		writeError(w, http.StatusConflict, "this session has no resolved flow definition")
		return session.Session{}, nil, appCaller{}, false
	}
	// A step outside the flow collects nothing: its evidence would be stored
	// and re-verified at the finish though the flow never asked for it.
	inFlow := step
	if isFaceStep(step) {
		inFlow = flow.Step(faceStepName(resolvedFlow))
	}
	if !slices.Contains(resolvedFlow.Steps, inFlow) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("step %q is not part of this session's flow", step))
		return session.Session{}, nil, appCaller{}, false
	}
	return sess, resolvedFlow, caller, true
}

// errStepAlreadyRecorded: a step's evidence is never replaced, save a failed
// face step's (faceStepRetryable).
var errStepAlreadyRecorded = errors.New("step already recorded")

// maxFaceStepAttempts bounds how often one session's face step is submitted.
// How often the subject may retry a failed liveness or match is the app's
// call; this is only the engine's safety bound on the Regula transactions a
// session runs. Past it the last failed evidence stands and the submit
// decides on it.
const maxFaceStepAttempts = 3

// faceStepRetryable reports whether ev, recorded face evidence, may be
// replaced by a new attempt: it failed liveness or did not match, and the
// session has attempts left.
func faceStepRetryable(ev *session.SelfieStepEvidence) bool {
	if ev == nil {
		return false
	}
	failed := !ev.LivenessPassed || (ev.FaceVerified != nil && !*ev.FaceVerified)
	return failed && faceStepAttempts(ev) < maxFaceStepAttempts
}

// faceStepAttempts is how many times ev's face step was submitted; 0 without
// evidence.
func faceStepAttempts(ev *session.SelfieStepEvidence) int {
	if ev == nil {
		return 0
	}
	return max(ev.Attempts, 1)
}

// stepWriteCheck reports whether caller may write step's evidence: it holds its
// slot, the session is open and undecided, and the step has no evidence yet
// (or failed face evidence it may replace). The check inside the write's
// Update is the one that counts.
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
	if step != "" && stepHasEvidence(sess, step) && (!isFaceStep(step) || !faceStepRetryable(sess.Steps.Selfie)) {
		return errStepAlreadyRecorded
	}
	return sessionOpenForDevices(sess)
}

// writeStep stores one step's evidence once stepWriteCheck passes inside the
// Update, so a request that was overtaken (handed over, expired, recorded
// meanwhile) cannot write.
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

// stepTiming stamps a step with server time. An existing StartedAt is kept, so
// it says when the subject first reached the step.
func stepTiming(existing *session.StepTiming, now time.Time) session.StepTiming {
	startedAt := now
	if existing != nil && !existing.StartedAt.IsZero() {
		startedAt = existing.StartedAt
	}
	return session.StepTiming{StartedAt: startedAt, SubmittedAt: now}
}

// advanceToInProgress moves sess to in_progress, through opened when it never
// was (there is no direct created to in_progress transition).
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

// writeStepRejected answers a step for a session that can no longer take one:
// 410 when expired, 409 once it has an outcome.
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

// stepResponse is every step endpoint's answer: the step is stored, and
// CurrentStep is what comes next ("" once complete).
type stepResponse struct {
	Status         session.Status `json:"status"`
	CompletedSteps []string       `json:"completedSteps"`
	CurrentStep    *string        `json:"currentStep,omitempty"`
	Lifecycle      string         `json:"lifecycle"`
	ErrorCode      string         `json:"errorCode,omitempty"`
	ReadyToSubmit  bool           `json:"readyToSubmit,omitempty"`
	// AlreadyRecorded: the step (or the submit) was already recorded, so nothing
	// was stored.
	AlreadyRecorded bool `json:"alreadyRecorded,omitempty"`
}

func (s *Server) stepResponseFor(sess session.Session, fd *flow.FlowDefinition) stepResponse {
	return stepResponse{
		Status: sess.Status, CompletedSteps: sess.CompletedSteps(), ErrorCode: sess.ErrorCode,
		CurrentStep: currentStep(sess, fd), Lifecycle: sessionLifecycle(sess), ReadyToSubmit: readyToSubmit(sess, fd),
	}
}

func readyToSubmit(sess session.Session, fd *flow.FlowDefinition) bool {
	return fd != nil && sessionOpenForDevices(sess) == nil && requiredStepsComplete(sess, fd)
}

// ---- selfie / liveness / face_match -----------------------------------------

const (
	faceProviderRegula             = "regula"                    // SelfieStepEvidence.Provider, biometricsInfo.Engine
	errCodeFaceProviderUnavailable = "FACE_PROVIDER_UNAVAILABLE" // Regula unreachable or not configured
)

// submitSelfieStepRequest is the face step: the Regula liveness transaction the
// app ran, or an image with a burst of frames for a provider that takes one.
// Exactly one of LivenessTransactionID and Image is set.
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

// handleSubmitSelfieStep is the face steps, one live capture for all of them.
// The reference (the chip portrait, or the customer's photo on a flow without
// nfc_read) has landed by then, so the match is made here.
func (s *Server) handleSubmitSelfieStep(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
	var req submitSelfieStepRequest
	decodeErr := json.NewDecoder(r.Body).Decode(&req)
	sess, resolvedFlow, caller, ok := s.stepSession(w, r, flow.StepSelfie)
	if !ok {
		return
	}
	// The session's own Regula transaction is released on every path from
	// here, a refused submission included; Release leaves another session's
	// alone, and an unauthenticated request never reaches it.
	if decodeErr == nil && req.LivenessTransactionID != "" && s.cfg.Regula != nil {
		defer regulaFaceVerifier{client: s.cfg.Regula}.Release(r.Context(), sess, liveCapture{TransactionID: req.LivenessTransactionID})
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
		raw, mime, err := decodeImageBase64(req.Image, firstNonEmpty(req.MimeType, defaultImageMime))
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
		writeFaceVerifyError(w, r, provider, err)
		return
	}

	// Liveness and the match take seconds; writeStep checks again that nothing
	// overtook this request.
	stage := faceStepName(resolvedFlow)
	updated, err := s.writeStep(sess, caller, flow.Step(stage), func(sess *session.Session, now time.Time) {
		// A retry replaces a failed attempt (faceStepRetryable): the step
		// started with the first.
		var started *session.StepTiming
		if prev := sess.Steps.Selfie; prev != nil {
			started = &prev.Timing
		}
		ev := &session.SelfieStepEvidence{
			LivenessPassed: out.LivenessPassed, LivenessScore: out.LivenessScore,
			FaceMatchScore: out.MatchScore, FaceVerified: out.Matched,
			Attempts: faceStepAttempts(sess.Steps.Selfie) + 1,
			Timing:   stepTiming(started, now),
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
	submitted["attempt"] = faceStepAttempts(updated.Steps.Selfie)
	s.auditStepSubmitted(updated, resolvedFlow, stage, submitted)
	resp := selfieStepResponse{stepResponse: s.stepResponseFor(updated, resolvedFlow)}
	resp.Biometrics = &biometricsInfo{
		FaceMatchScore: out.MatchScore, FaceVerified: out.Matched,
		LivenessResult: livenessResult(out.LivenessPassed), LivenessScore: out.LivenessScore,
		Engine: selfieEngine(updated.Steps.Selfie), Threshold: out.Threshold,
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeFaceVerifyError maps a FaceVerifier error to its HTTP response.
func writeFaceVerifyError(w http.ResponseWriter, r *http.Request, provider flow.FaceProvider, err error) {
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
		writeInternalError(w, r, "face verification failed", err)
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

func livenessResult(passed bool) string {
	if passed {
		return "passed"
	}
	return "failed"
}

// faceMatchReference is what the face is matched against: the customer's
// reference photo (a flow without nfc_read), or the chip's DG2 portrait. ok is
// false until that has landed; the server does not rely on the app's order.
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
	if flow.DrivingLicence(req.MrtdEvidence.DocumentType) {
		writeDocumentUnsupported(w)
		return
	}
	if _, err := verifyMrtdEvidence(req.MrtdEvidence, sess.AAChallenge); err != nil {
		writeError(w, http.StatusBadRequest, "invalid mrtdEvidence: "+err.Error())
		return
	}
	// The document is read off the chip evidence, never taken from the
	// app's own copy: see documentFromEvidence.
	chipDoc, err := documentFromEvidence(req.MrtdEvidence, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid mrtdEvidence: "+err.Error())
		return
	}
	req.Document = chipDoc
	// The photo is the face_match reference (faceMatchReference), so it must
	// be the chip's own portrait: see photoFromChip.
	if req.Photo != nil && !photoFromChip(req.Photo, req.MrtdEvidence) {
		writeError(w, http.StatusBadRequest, "photo is not the portrait in mrtdEvidence's DG2")
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
	// policy rather than kept possibly readable.
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
	raw, err := marshalToMap(req)
	if err != nil {
		writeInternalError(w, r, "could not store nfc evidence", err)
		return
	}

	hasDocumentCapture := slices.Contains(resolvedFlow.Steps, flow.StepDocumentCapture)
	updated, err := s.writeStep(sess, caller, flow.StepNFCRead, func(sess *session.Session, now time.Time) {
		sess.Steps.NFC = &session.NFCStepEvidence{Raw: raw, Timing: stepTiming(nil, now)}
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

// photoFromChip reports whether photo's bytes are inside ev's DG2. Passive
// Authentication covers DG2 but not the separately sent photo; without this a
// genuine chip could be paired with any face.
func photoFromChip(photo *photoInfo, ev *mrtdEvidenceRequest) bool {
	raw, _, err := decodeImageBase64(photo.ImageBase64, photo.MimeType)
	if err != nil || len(raw) == 0 {
		return false
	}
	dg2, err := hex.DecodeString(ev.DataGroups[dataGroupPortrait])
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

func requiredStepsComplete(sess session.Session, fd *flow.FlowDefinition) bool {
	for _, step := range fd.Steps {
		if !stepHasEvidence(sess, step) {
			return false
		}
	}
	return true
}

// stepHasEvidence reports whether sess holds step's evidence; the one face
// capture covers every face step.
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

// faceStepName is the face step's name in fd, so the audit log names it as the
// flow does.
func faceStepName(fd *flow.FlowDefinition) string {
	for _, step := range fd.Steps {
		switch step {
		case flow.StepFaceVerification, flow.StepSelfie, flow.StepLiveness, flow.StepFaceMatch:
			return string(step)
		}
	}
	return string(flow.StepSelfie)
}

var errStepAlreadyStarted = errors.New("step already started")

// markStepStarted records the subject beginning step: in_progress and one
// event per step. Repeats, and steps with evidence, are no-ops, so it is cheap
// enough for a per-frame endpoint.
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

// handleStartStep is the signal that the subject began a step of the flow
// (the app opening its camera or the chip read, the browser its camera), so the
// audit log shows each step as it starts. Idempotent.
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
		writeInternalError(w, r, "could not reload session", err)
		return
	}
	writeJSON(w, http.StatusOK, s.stepResponseFor(current, resolvedFlow))
}

// appStepRequest is the shared start of the /steps/{step}/... routes: the
// session, its flow and the step, with any face step name mapped to the flow's
// own.
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
		writeInternalError(w, r, "could not resolve flow", err)
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

type stepResultResponse struct {
	stepResultView
	stepResponse
}

// handleStepResult is one step's stored verdicts, never its evidence: how a
// reloaded or handed-over client confirms a step landed. Readable after the
// session finished.
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

func isFaceStep(step flow.Step) bool {
	switch step {
	case flow.StepSelfie, flow.StepLiveness, flow.StepFaceMatch, flow.StepFaceVerification:
		return true
	}
	return false
}

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

// errStepsIncomplete: a submit before every step has evidence, or a reset while
// the result was being built.
var errStepsIncomplete = errors.New("not every step of this session has a result yet")

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
			// The DG11 extras, and its name over the MRZ's.
			req.Document.PersonalNumber = firstNonEmpty(nfcReq.Document.PersonalNumber, req.Document.PersonalNumber)
			req.Document.PlaceOfBirth = nfcReq.Document.PlaceOfBirth
			if nfcReq.Document.DisplayName != "" {
				req.Document.DisplayName = nfcReq.Document.DisplayName
			}
		}
	}

	bsnPolicy, redactionPolicy := effectivePrivacyPolicy(fd)
	result := buildResult(sess, req, verifiedChecks, fd, bsnPolicy, redactionPolicy)

	achieved := computeEIDASAssuranceLevel(fd, req, verifiedChecks, sess.ReferencePhoto == "")
	finalStatus, finalErrorCode, reason := sessionOutcome(fd, sess, req, verifiedChecks, achieved, s.cfg.Now())

	snapshotResets, snapshotFaceAttempts := sess.ResetCount, faceStepAttempts(sess.Steps.Selfie)
	updated, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		if err := checkAppWrite(sess, caller); err != nil {
			return err
		}
		// The result was built from the snapshot above: a reset in between means it is
		// not this session's evidence any more, even if new evidence completed the
		// steps again; nor is it once a face retry replaced the face evidence.
		if sess.ResetCount != snapshotResets || faceStepAttempts(sess.Steps.Selfie) != snapshotFaceAttempts ||
			!requiredStepsComplete(*sess, fd) {
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
	submitted["stage"] = "session_submit"
	s.auditProofing(updated, eventResultSubmitted, submitted)
	outcomeDetails := map[string]any{}
	if updated.ErrorCode != "" {
		outcomeDetails["errorCode"] = updated.ErrorCode
	}
	if reason != "" {
		outcomeDetails["reason"] = reason
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
)

// sessionOutcome decides a finished session of fd: rejected for a tampered or
// cloned chip, a submission outside what fd accepts, a face step that did not
// verify the person, or an achieved eIDAS level below fd's required one, in
// that order; approved otherwise. The required level is only that minimum: it
// neither raises nor caps achieved, which is computed without it.
func sessionOutcome(fd *flow.FlowDefinition, sess session.Session, req appResultRequest, checks *chipChecksInfo, achieved flow.AssuranceLevel, now time.Time) (status session.Status, errorCode, reason string) {
	if failed, code := authenticityFailure(fd, checks); failed {
		return session.StatusRejected, code, reasonTamperDetected
	}
	if failed, code := flowComplianceFailure(fd, sess, req, now); failed {
		return session.StatusRejected, code, reasonFlowPolicyViolation
	}
	if failed, code := faceStepFailure(fd, req, checks); failed {
		return session.StatusRejected, code, reasonCheckFailed
	}
	if !flow.MeetsLevel(achieved, fd.RequiredAssuranceLevel) {
		return session.StatusRejected, errCodeAssuranceNotMet, reasonAssuranceNotMet
	}
	return session.StatusApproved, "", ""
}

// redactStepsForStorage applies the BSN and redaction policies to the stored
// steps. It runs only after buildResult: the face match reads the raw chip
// portrait, so redacting earlier would match against a blurred image. The raw
// DG1 is dropped too; Passive Authentication already ran on it.
func redactStepsForStorage(sess *session.Session, bsnPolicy privacy.BSNPolicy, redaction privacy.RedactionPolicy) {
	if redaction.BlurBSN {
		redactStoredDocumentPhoto(sess)
	}
	if redaction.BlurFace && sess.Steps.Selfie != nil && sess.Steps.Selfie.Image != "" {
		// A copy, not a write through the pointer (see handleSubmitNFCStep).
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
	if err != nil {
		return
	}
	changed := false
	if redaction.BlurFace && req.Photo != nil && req.Photo.ImageBase64 != "" {
		if blurred, mime, ok := blurFace(req.Photo.ImageBase64, req.Photo.MimeType); ok {
			req.Photo.ImageBase64, req.Photo.MimeType = blurred, mime
		} else {
			slog.Warn("identity proofing: could not blur the stored chip photo; dropping it", slog.String("session_id", sess.ID))
			req.Photo = nil
		}
		changed = true
	}
	if req.MrtdEvidence != nil {
		// The raw data groups are the same face and the same BSN again, as
		// the chip's own bytes: no redacted encoding of them exists, so they
		// go. Passive Authentication needed them; the result is built.
		if redaction.BlurFace {
			changed = dropDataGroups(req.MrtdEvidence, dataGroupPortrait, dataGroupDLPortrait) || changed
		}
		if p := bsnPolicy.Effective(); (p == privacy.BSNPolicyMask || p == privacy.BSNPolicyOmit) && dutchDocument(*sess, req) {
			// Older Dutch documents carry the BSN in the MRZ's optional data
			// (DG1) as well as in DG11.
			changed = dropDataGroups(req.MrtdEvidence, dataGroupMRZ, dataGroupPersonalDetails) || changed
		}
	}
	if !changed {
		return
	}
	raw, err := marshalToMap(req)
	if err != nil {
		// Never keep the unredacted original: without a re-marshalled copy,
		// drop the stored chip evidence's raw form altogether.
		slog.Warn("identity proofing: could not store the redacted chip evidence", slog.String("session_id", sess.ID), slog.Any("error", err))
		raw = nil
	}
	nfc := *sess.Steps.NFC
	nfc.Raw = raw
	sess.Steps.NFC = &nfc
}

const (
	dataGroupMRZ             = "DG1"
	dataGroupPortrait        = "DG2"
	dataGroupDLPortrait      = "DG6"
	dataGroupPersonalDetails = "DG11"
	// dataGroupAAKey is a passport's or ID card's Active Authentication key.
	dataGroupAAKey = "DG15"
)

func dropDataGroups(ev *mrtdEvidenceRequest, names ...string) bool {
	dropped := false
	for _, name := range names {
		if _, ok := ev.DataGroups[name]; ok {
			delete(ev.DataGroups, name)
			dropped = true
		}
	}
	return dropped
}

func dutchDocument(sess session.Session, req submitNFCStepRequest) bool {
	if req.Document != nil && req.Document.IssuingState != "" {
		return req.Document.IssuingState == dutchIssuingState
	}
	return sess.Steps.Document != nil && sess.Steps.Document.Parsed.IssuingState == dutchIssuingState
}

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

// redactStoredDocumentPhoto covers the printed BSN in the stored photos as
// buildResult did in the result, and drops a photo it could not cover.
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

// blurFace pixelates a face under a BlurFace policy, including a chip's
// JPEG2000 portrait. ok is false when it could not, and the caller then drops
// the image.
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

func documentInfoFromParsedMRZ(p session.ParsedMRZ) documentInfo {
	valid := p.AllChecksValid
	notExpired := !documentExpired(p.DateOfExpiry, time.Now())
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

// documentExpired reports whether a yyyyMMdd expiry date has passed on now's
// date (UTC): a document is valid through its expiry day. An unreadable date
// is expired.
func documentExpired(yyyyMMdd string, now time.Time) bool {
	expiry, err := time.Parse(time.DateOnly, yyyyMMdd)
	if err != nil {
		return true
	}
	return expiry.Before(now.UTC().Truncate(day))
}
