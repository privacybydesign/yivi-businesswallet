package proofingengine

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

func TestFailedFaceStepRetryable(t *testing.T) {
	matched, mismatched := true, false
	for _, tc := range []struct {
		name string
		ev   *session.SelfieStepEvidence
		want error
	}{
		{"no evidence", nil, nil},
		{"failed liveness", &session.SelfieStepEvidence{Attempts: 1}, nil},
		{"evidence before attempts were counted", &session.SelfieStepEvidence{}, nil},
		{"no match", &session.SelfieStepEvidence{LivenessPassed: true, FaceVerified: &mismatched, Attempts: 2}, nil},
		{"passed", &session.SelfieStepEvidence{LivenessPassed: true, FaceVerified: &matched, Attempts: 1}, errStepAlreadyRecorded},
		{"attempts spent", &session.SelfieStepEvidence{Attempts: maxFaceStepAttempts}, errStepAlreadyRecorded},
	} {
		sess := inProgress("ps_1")
		sess.Steps.Selfie = tc.ev
		if err := stepWriteCheck(sess, appCaller{}, flow.StepFaceVerification); !errors.Is(err, tc.want) {
			t.Errorf("%s: stepWriteCheck = %v, want %v", tc.name, err, tc.want)
		}
	}
}

// TestFaceRetryAfterDecision checks that a failed, still retryable face step
// cannot be retried once the session was submitted and decided.
func TestFaceRetryAfterDecision(t *testing.T) {
	for _, status := range []session.Status{session.StatusApproved, session.StatusRejected, session.StatusNeedsReview} {
		sess := inProgress("ps_1")
		sess.Status = status
		sess.Steps.Selfie = &session.SelfieStepEvidence{Attempts: 1}
		if err := stepWriteCheck(sess, appCaller{}, flow.StepFaceVerification); !errors.Is(err, errSessionComplete) {
			t.Errorf("%s: stepWriteCheck = %v, want errSessionComplete", status, err)
		}
	}
}

// faceOnlySession is ready to submit on faceOnlyFlow: a passed face step
// against the org's photo.
func faceOnlySession(id string) session.Session {
	matched, score := true, 0.9
	sess := inProgress(id)
	sess.ReferencePhoto, sess.ReferencePhotoMime = base64.StdEncoding.EncodeToString([]byte("photo")), defaultImageMime
	sess.Steps.Selfie = &session.SelfieStepEvidence{LivenessPassed: true, FaceVerified: &matched, FaceMatchScore: &score, Attempts: 1}
	return sess
}

var faceOnlyFlow = &flow.FlowDefinition{Name: "Face", Steps: []flow.Step{flow.StepFaceVerification}}

// TestFinishStaleSnapshot checks that a result built from a snapshot
// is not stored once the session was reset, or its face evidence replaced by
// a retry, in between.
func TestFinishStaleSnapshot(t *testing.T) {
	for name, change := range map[string]func(*session.Session){
		"reset":      func(s *session.Session) { s.ResetCount++ },
		"face retry": func(s *session.Session) { s.Steps.Selfie.Attempts++ },
	} {
		snapshot := faceOnlySession("ps_" + name)
		stored := faceOnlySession("ps_" + name)
		change(&stored)
		s := New(DefaultConfig(), newMemSessions(stored), nil, nil, nil)
		if _, err := s.finishSession(context.Background(), snapshot, faceOnlyFlow, appCaller{}); !errors.Is(err, errStepsIncomplete) {
			t.Errorf("%s: finishSession = %v, want errStepsIncomplete", name, err)
		}
	}
	// The same snapshot without a change in between finishes.
	sess := faceOnlySession("ps_same")
	s := New(DefaultConfig(), newMemSessions(sess), nil, nil, nil)
	if got, err := s.finishSession(context.Background(), sess, faceOnlyFlow, appCaller{}); err != nil || !got.Status.Terminal() {
		t.Errorf("finishSession = %s, %v; want an outcome", got.Status, err)
	}
}
