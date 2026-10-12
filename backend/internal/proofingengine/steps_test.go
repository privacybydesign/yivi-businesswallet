package proofingengine

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regula"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

func TestFailedFaceStepRetryable(t *testing.T) {
	matched, mismatched := true, false
	for _, tc := range []struct {
		name     string
		ev       *session.SelfieStepEvidence
		reserved int
		want     error
	}{
		{"no evidence", nil, 0, nil},
		{"failed liveness", &session.SelfieStepEvidence{Attempts: 1}, 1, nil},
		{"evidence before attempts were counted", &session.SelfieStepEvidence{}, 0, nil},
		{"no match", &session.SelfieStepEvidence{LivenessPassed: true, FaceVerified: &mismatched, Attempts: 2}, 2, nil},
		{"passed", &session.SelfieStepEvidence{LivenessPassed: true, FaceVerified: &matched, Attempts: 1}, 1, errStepAlreadyRecorded},
		{"attempts spent", &session.SelfieStepEvidence{Attempts: maxFaceStepAttempts}, maxFaceStepAttempts, errStepAlreadyRecorded},
		{"attempts reserved by others", &session.SelfieStepEvidence{Attempts: 1}, maxFaceStepAttempts, errStepAlreadyRecorded},
		{"attempts reserved, no evidence", nil, maxFaceStepAttempts, errFaceAttemptsSpent},
	} {
		sess := inProgress("ps_1")
		sess.Steps.Selfie, sess.FaceAttempts = tc.ev, tc.reserved
		if err := stepWriteCheck(sess, appCaller{}, flow.StepFaceVerification); !errors.Is(err, tc.want) {
			t.Errorf("%s: stepWriteCheck = %v, want %v", tc.name, err, tc.want)
		}
	}
}

// TestReservedFaceAttemptWrites checks that an attempt holding a reservation
// may store its result once the reservations reached the bound, over failed
// evidence but never over passed evidence.
func TestReservedFaceAttemptWrites(t *testing.T) {
	matched := true
	sess := inProgress("ps_1")
	sess.FaceAttempts = maxFaceStepAttempts
	if err := stepWriteCheckAs(sess, appCaller{}, flow.StepFaceVerification, true); err != nil {
		t.Errorf("no evidence: stepWriteCheckAs = %v, want nil", err)
	}
	sess.Steps.Selfie = &session.SelfieStepEvidence{Attempts: 1}
	if err := stepWriteCheckAs(sess, appCaller{}, flow.StepFaceVerification, true); err != nil {
		t.Errorf("failed evidence: stepWriteCheckAs = %v, want nil", err)
	}
	sess.Steps.Selfie = &session.SelfieStepEvidence{LivenessPassed: true, FaceVerified: &matched, Attempts: 1}
	if err := stepWriteCheckAs(sess, appCaller{}, flow.StepFaceVerification, true); !errors.Is(err, errStepAlreadyRecorded) {
		t.Errorf("passed evidence: stepWriteCheckAs = %v, want errStepAlreadyRecorded", err)
	}
}

// heldMatchRegula confirms every liveness transaction tagged tag, and holds
// each face match until release is closed; past maxFaceStepAttempts matches
// it closes overflow.
type heldMatchRegula struct {
	facelessRegula
	tag      string
	matches  atomic.Int64
	release  chan struct{}
	overflow chan struct{}
	once     sync.Once
}

func (f *heldMatchRegula) GetLiveness(context.Context, string) (regula.LivenessTransaction, error) {
	confirmed := 0
	return regula.LivenessTransaction{Status: &confirmed, Tag: f.tag}, nil
}

func (f *heldMatchRegula) Match(context.Context, string, string) (regula.MatchResult, error) {
	if f.matches.Add(1) > maxFaceStepAttempts {
		f.once.Do(func() { close(f.overflow) })
	}
	<-f.release
	return regula.MatchResult{}, nil
}

// singleFlow is a flow.Store holding one flow definition.
type singleFlow struct {
	flow.Store
	fd flow.FlowDefinition
}

func (f singleFlow) Get(context.Context, string, string, int) (flow.FlowDefinition, error) {
	return f.fd, nil
}

// TestConcurrentFaceAttemptsBounded checks that concurrent face step
// submissions from one device run at most maxFaceStepAttempts Regula
// matches: each reserves its attempt before Regula is called.
func TestConcurrentFaceAttemptsBounded(t *testing.T) {
	const submissions = 3 * maxFaceStepAttempts
	const deviceToken = "device-token"
	now := time.Now().UTC()
	sess := faceOnlySession("ps_1")
	sess.Steps.Selfie = nil
	sess.Flow, sess.FlowVersion, sess.ExpiresAt = "flow", 1, now.Add(time.Hour)
	sess.Access.Native = &session.DeviceAccess{
		ID: "dev", TokenHash: session.HashAccessToken(deviceToken), State: session.DeviceStateActive, ClaimedAt: now, LastActiveAt: now,
	}
	fake := &heldMatchRegula{tag: regulaTag(sess), release: make(chan struct{}), overflow: make(chan struct{})}
	cfg := DefaultConfig()
	cfg.Regula = fake
	fd := flow.FlowDefinition{ID: "flow", Version: 1, Name: "Face", Steps: []flow.Step{flow.StepFaceVerification}, FaceProvider: flow.FaceProviderRegula}
	s := New(cfg, newMemSessions(sess), singleFlow{fd: fd}, nil, nil)
	mux := http.NewServeMux()
	s.Register(mux)

	codes := make(chan int, submissions)
	for i := range submissions {
		go func() {
			body := `{"livenessTransactionId":"tx-` + strconv.Itoa(i) + `"}`
			req := httptest.NewRequest(http.MethodPost, "/app/"+sess.Token+"/steps/selfie", strings.NewReader(body))
			req.Header.Set(deviceTokenHeader, deviceToken)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			codes <- rec.Code
		}()
	}
	// The submissions past the bound are refused while the reserved ones
	// are still held at Regula.
	for range submissions - maxFaceStepAttempts {
		select {
		case code := <-codes:
			if code != http.StatusConflict {
				t.Errorf("submission past the bound = %d, want %d", code, http.StatusConflict)
			}
		case <-fake.overflow:
			close(fake.release)
			t.Fatalf("more than %d Regula matches", maxFaceStepAttempts)
		}
	}
	close(fake.release)
	for range maxFaceStepAttempts {
		if code := <-codes; code != http.StatusOK {
			t.Errorf("reserved submission = %d, want %d", code, http.StatusOK)
		}
	}
	if got := fake.matches.Load(); got != maxFaceStepAttempts {
		t.Errorf("Regula matches = %d, want %d", got, maxFaceStepAttempts)
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
// is not stored once its face evidence was replaced by a retry in between.
func TestFinishStaleSnapshot(t *testing.T) {
	for name, change := range map[string]func(*session.Session){
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
