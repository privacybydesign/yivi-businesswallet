package proofingengine

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

const (
	faceProviderRegula             = "regula"                    // SelfieStepEvidence.Provider, biometricsInfo.Engine
	errCodeFaceProviderUnavailable = "FACE_PROVIDER_UNAVAILABLE" // Regula unreachable or not configured
)

// submitSelfieStepRequest is the face step: the Regula liveness transaction the
// app ran.
type submitSelfieStepRequest struct {
	LivenessTransactionID string `json:"livenessTransactionId,omitempty"`
}

type selfieStepResponse struct {
	stepResponse
	Biometrics *biometricsInfo `json:"biometrics,omitempty"`
}

// handleSubmitSelfieStep is the face steps, one live capture for all of them.
// The reference (the chip portrait, or the customer's photo on a flow without
// nfc_read) has landed by then, so the match is made here.
func (s *Server) handleSubmitSelfieStep(w http.ResponseWriter, r *http.Request) {
	sess, resolvedFlow, caller, ok := s.stepSession(w, r, flow.StepSelfie)
	if !ok {
		return
	}
	var req submitSelfieStepRequest
	if !s.decode(w, r, &req) {
		return
	}
	if req.LivenessTransactionID == "" {
		writeError(w, http.StatusBadRequest, "livenessTransactionId is required")
		return
	}
	live := liveCapture{TransactionID: req.LivenessTransactionID}
	// The session's own Regula transaction is released on every path from
	// here, a refused submission included; Release leaves another session's
	// alone, and an unauthenticated request never reaches it.
	if s.cfg.Regula != nil {
		defer regulaFaceVerifier{client: s.cfg.Regula}.Release(r.Context(), sess, live)
	}
	provider := s.faceProviderFor(resolvedFlow)
	verifier, ok := s.faceVerifier(provider, resolvedFlow)
	if !ok {
		writeFaceProviderUnavailable(w)
		return
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

	stage := faceStepName(resolvedFlow)
	attempt, err := s.reserveFaceAttempt(sess, caller, flow.Step(stage))
	if err != nil {
		s.writeStepError(w, r, sess, resolvedFlow, caller, flow.Step(stage), err)
		return
	}
	out, err := verifier.Verify(r.Context(), sess, ref, live)
	if err != nil {
		if refundsFaceAttempt(err) {
			s.refundFaceAttempt(sess, attempt)
		}
		writeFaceVerifyError(w, r, provider, err)
		return
	}

	// Liveness and the match take seconds; writeStepAs checks again that
	// nothing overtook this request.
	updated, err := s.writeStepAs(sess, caller, flow.Step(stage), true, func(sess *session.Session, now time.Time) {
		// A retry replaces a failed attempt (faceStepRetryable): the step
		// started with the first.
		var started *session.StepTiming
		if prev := sess.Steps.Selfie; prev != nil {
			started = &prev.Timing
		}
		ev := &session.SelfieStepEvidence{
			LivenessPassed: out.LivenessPassed, LivenessScore: out.LivenessScore,
			FaceMatchScore: out.MatchScore, FaceVerified: out.Matched,
			Attempts: attempt,
			Timing:   stepTiming(started, now),
		}
		// Regula holds the capture itself and it is deleted with the
		// transaction: the selfie kept is the live face's crop from the match.
		ev.Provider = faceProviderRegula
		ev.Image, ev.MimeType = out.Selfie, out.SelfieMime
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
		LivenessResult: livenessResults[out.LivenessPassed], LivenessScore: out.LivenessScore,
		Engine: selfieEngine(updated.Steps.Selfie), Threshold: out.Threshold,
	}
	writeJSON(w, http.StatusOK, resp)
}

// reserveFaceAttempt spends one of sess's face attempts before the provider is
// called, inside the Update, so concurrent submissions never run more than
// maxFaceStepAttempts. attempt is its number.
func (s *Server) reserveFaceAttempt(sess session.Session, caller appCaller, step flow.Step) (attempt int, err error) {
	_, err = s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		if err := stepWriteCheck(*sess, caller, step); err != nil {
			return err
		}
		attempt = faceAttemptsReserved(*sess) + 1
		sess.FaceAttempts = attempt
		return nil
	})
	return attempt, err
}

// refundsFaceAttempt is whether a face check that failed with err gives its
// attempt back. Regula failing, or no call made at all (a capture of the
// wrong kind, a reference that does not decode), is not the subject's
// attempt, and three of those would otherwise strand the session. A
// transaction Regula does not know, or one that is another session's, is the
// client's doing and stays spent, so made-up ids cannot run up Face API calls.
func refundsFaceAttempt(err error) bool {
	return !errors.Is(err, errUnknownTransaction) && !errors.Is(err, errForeignTransaction)
}

// refundFaceAttempt gives back attempt, reserved for a face check that failed
// through no fault of the subject's: only while it is still the latest
// reservation and no evidence was written for it. A failed refund is logged;
// the request is answered with the check's error either way.
func (s *Server) refundFaceAttempt(sess session.Session, attempt int) {
	_, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		if sess.FaceAttempts == attempt && faceStepAttempts(sess.Steps.Selfie) < attempt {
			sess.FaceAttempts = attempt - 1
		}
		return nil
	})
	if err != nil {
		slog.Warn("identity proofing: refund a face attempt", slog.String("session_id", sess.ID), slog.Any("error", err))
	}
}

// writeFaceVerifyError maps a faceVerifier error to its HTTP response.
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

// A liveness result as reported, and livenessResults the one a check's
// outcome reports.
const (
	livenessPassed = "passed"
	livenessFailed = "failed"
)

var livenessResults = map[bool]string{true: livenessPassed, false: livenessFailed}

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
