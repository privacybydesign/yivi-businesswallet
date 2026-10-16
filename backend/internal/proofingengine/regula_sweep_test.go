package proofingengine

import (
	"context"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
	pp "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

func TestSessionEnd(t *testing.T) {
	now := time.Now()
	completed := now.Add(-time.Minute)
	for _, tc := range []struct {
		name  string
		sess  session.Session
		end   time.Time
		ended bool
	}{
		{"running", session.Session{Status: session.StatusInProgress}, time.Time{}, false},
		{"approved", session.Session{Status: session.StatusApproved, CompletedAt: &completed}, completed, true},
		{"rejected", session.Session{Status: session.StatusRejected, CompletedAt: &completed}, completed, true},
		{"cancelled", session.Session{Status: session.StatusCancelled, CompletedAt: &completed}, completed, true},
		{"in review", session.Session{Status: session.StatusNeedsReview}, now, true},
	} {
		end, ended := sessionEnd(tc.sess, now)
		if ended != tc.ended || !end.Equal(tc.end) {
			t.Errorf("%s: sessionEnd = %v, %v; want %v, %v", tc.name, end, ended, tc.end, tc.ended)
		}
	}
}

func TestDeleteSweepsRegulaNow(t *testing.T) {
	sess := inProgress("ps_1")
	queue := &settledQueue{}
	cfg := DefaultConfig()
	cfg.RegulaSweeps = queue
	s := New(cfg, newMemSessions(sess), nil, nil, nil)
	before := time.Now().UTC()
	if err := s.DeleteSession(context.Background(), pp.Tenant{ID: sess.TenantID}, sess.ID, sess.Token); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if queue.tag != regulaTag(sess) || queue.dueAt.Before(before) || queue.dueAt.After(time.Now().UTC()) {
		t.Errorf("sweep queued %q due %v; want %q due now", queue.tag, queue.dueAt, regulaTag(sess))
	}
}
