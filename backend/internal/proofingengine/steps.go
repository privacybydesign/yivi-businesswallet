// The Idem app's step endpoints (/api/v1/app/{token}/steps/...). A session
// collects its evidence one step at a time, so a device taking it over resumes
// where the last stopped. A step never finishes the session: once every step
// has evidence the session is readyToSubmit, and POST .../submit lets the
// server decide the outcome (finishSession). document_capture (the MRZ the app
// scanned) and nfc_read (the chip) always come together.
package proofingengine

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
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
	details[detailStage], details[detailRoute] = string(step), r.Pattern
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
		details[detailReason], details[detailRoute] = accessErrorCode(err), r.Pattern
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
