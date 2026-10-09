package proofingengine

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
	pp "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// TestFacelessFramesSpendBudget checks that frames without a face cost no
// attempt but do cost a Regula call, so the session's budget ends the face
// check.
func TestFacelessFramesSpendBudget(t *testing.T) {
	sess := inProgress("ps_1")
	sess.Method = session.MethodBiometricBoundLogin
	sess.Yivi = &session.YiviState{Reference: base64.StdEncoding.EncodeToString([]byte("ref")), FaceStarted: true}
	client := &facelessRegula{}
	cfg := DefaultConfig()
	cfg.Regula, cfg.BoundLoginStableFrames, cfg.BoundLoginMaxAttempts = client, 1, 2
	store := newMemSessions(sess)
	s := New(cfg, store, nil, nil, nil)
	budget := s.boundLoginFrameBudget()
	for i := range budget {
		current, err := store.Get(sess.TenantID, sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		frame := base64.StdEncoding.EncodeToString([]byte("frame " + strconv.Itoa(i)))
		v, err := s.faceFrame(context.Background(), current, frame)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if last := i == budget-1; last != (v.Decision == pp.FaceDecisionRejected) {
			t.Fatalf("frame %d of %d = %s", i+1, budget, v.Decision)
		}
	}
	current, err := store.Get(sess.TenantID, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != session.StatusRejected || current.ErrorCode != errCodeFaceNoMatch {
		t.Errorf("session = %s %s, want rejected %s", current.Status, current.ErrorCode, errCodeFaceNoMatch)
	}
	if _, err := s.faceFrame(context.Background(), current, base64.StdEncoding.EncodeToString([]byte("one more"))); err == nil {
		t.Error("a frame after the budget was scored")
	}
	if got := client.calls.Load(); got != int64(budget) {
		t.Errorf("Regula called %d times, want the budget %d", got, budget)
	}
}

// concurrentFrames is how many frames TestConcurrentFramesBudget sends at
// once: well over the session's Regula budget.
const concurrentFrames = 50

// regulaCallHold keeps each Regula call of TestConcurrentFramesBudget in
// flight long enough for the other frames to arrive meanwhile.
const regulaCallHold = 20 * time.Millisecond

// TestConcurrentFramesBudget checks that frames sent at once, all from
// the same snapshot, reserve their Regula call under the row lock: no more
// calls go out than the session's budget.
func TestConcurrentFramesBudget(t *testing.T) {
	sess := inProgress("ps_1")
	sess.Method = session.MethodBiometricBoundLogin
	sess.Yivi = &session.YiviState{Reference: base64.StdEncoding.EncodeToString([]byte("ref")), FaceStarted: true}
	client := &facelessRegula{hold: regulaCallHold}
	cfg := DefaultConfig()
	cfg.Regula, cfg.BoundLoginStableFrames, cfg.BoundLoginMaxAttempts = client, 1, 2
	store := newMemSessions(sess)
	s := New(cfg, store, nil, nil, nil)
	var wg sync.WaitGroup
	errs := make(chan error, concurrentFrames)
	for i := range concurrentFrames {
		wg.Go(func() {
			frame := base64.StdEncoding.EncodeToString([]byte("frame " + strconv.Itoa(i)))
			if _, err := s.faceFrame(context.Background(), sess, frame); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		var refused *pp.RejectedError
		if !errors.As(err, &refused) {
			t.Errorf("frame: %v, want a refusal once the session is decided", err)
		}
	}
	if got, budget := client.calls.Load(), s.boundLoginFrameBudget(); got > int64(budget) {
		t.Errorf("Regula called %d times, over the budget %d", got, budget)
	}
	current, err := store.Get(sess.TenantID, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != session.StatusRejected || current.ErrorCode != errCodeFaceNoMatch {
		t.Errorf("session = %s %s, want rejected %s", current.Status, current.ErrorCode, errCodeFaceNoMatch)
	}
}
