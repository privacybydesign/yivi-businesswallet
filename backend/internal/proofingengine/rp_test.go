package proofingengine

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
	pp "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

func TestRetentionOverflowRefused(t *testing.T) {
	s := &Server{}
	spec := pp.FlowSpec{Name: "x", Steps: []string{"document_capture"}, RetentionOverrideSeconds: 1 << 62}
	var refused *pp.RejectedError
	if _, err := s.CreateFlow(context.Background(), pp.Tenant{ID: "org"}, spec); !errors.As(err, &refused) || refused.Status != http.StatusBadRequest {
		t.Errorf("CreateFlow(retention 1<<62 s) = %v, want 400", err)
	}
}

// The evidence type follows what the session did, not which attributes its
// flow releases: a chip read without chip_checks released is still eMRTD
// evidence, its checks read from the assurance; a photographed document is
// document_photo evidence.
func TestSessionIdentityEvidence(t *testing.T) {
	assurance := map[string]any{"categories": []any{
		map[string]any{"check": string(flow.CheckNFCPassiveAuth), "state": string(checkStatePass)},
		map[string]any{"check": string(flow.CheckNFCChipAuth), "state": string(checkStateFail)},
	}}
	chip := session.Session{
		ID: "chip", TenantID: "org", Token: "tok-chip", Status: session.StatusApproved,
		Steps:  session.StepEvidence{NFC: &session.NFCStepEvidence{}},
		Result: map[string]any{"assurance": assurance},
	}
	photo := session.Session{
		ID: "photo", TenantID: "org", Token: "tok-photo", Status: session.StatusApproved,
		Steps:  session.StepEvidence{DocumentPhoto: &session.DocumentPhotoStepEvidence{}},
		Result: map[string]any{"assurance": map[string]any{}},
	}
	s := &Server{sessions: newMemSessions(chip, photo)}
	for _, tc := range []struct {
		sess                  session.Session
		evidence, passive, aa string
	}{
		{chip, pp.EvidenceEMRTD, pp.CheckValid, pp.CheckInvalid},
		{photo, pp.EvidenceDocumentPhoto, pp.CheckNotPerformed, pp.CheckNotPerformed},
	} {
		id, err := s.SessionIdentity(context.Background(), pp.Tenant{ID: "org"}, tc.sess.ID, tc.sess.Token)
		if err != nil || id.Evidence == nil {
			t.Fatalf("%s: identity = %+v, %v", tc.sess.ID, id, err)
		}
		if ev := id.Evidence; ev.Type != tc.evidence || ev.PassiveAuth != tc.passive || ev.ActiveAuth != tc.aa {
			t.Errorf("%s: evidence = %s PA %s AA %s; want %s PA %s AA %s", tc.sess.ID, ev.Type, ev.PassiveAuth, ev.ActiveAuth, tc.evidence, tc.passive, tc.aa)
		}
	}
}

// A session's retention changes to a positive one, never to the engine's
// default: 0 would drop the flow's own.
func TestSetSessionsRetention(t *testing.T) {
	sessions := newMemSessions(session.Session{ID: "s1", TenantID: "org", RetentionOverride: time.Hour})
	s := New(DefaultConfig(), sessions, nil, nil, nil)
	tenant := pp.Tenant{ID: "org"}

	var refused *pp.RejectedError
	if err := s.SetSessionsRetention(context.Background(), tenant, []string{"s1"}, 0); !errors.As(err, &refused) || refused.Status != http.StatusBadRequest {
		t.Errorf("SetSessionsRetention(0) = %v, want 400", err)
	}
	if got, _ := sessions.Get("org", "s1"); got.RetentionOverride != time.Hour {
		t.Errorf("a refused call left retention %v, want the hour kept", got.RetentionOverride)
	}
	if err := s.SetSessionsRetention(context.Background(), tenant, []string{"s1"}, 48*time.Hour); err != nil {
		t.Fatalf("SetSessionsRetention: %v", err)
	}
	if got, _ := sessions.Get("org", "s1"); got.RetentionOverride != 48*time.Hour {
		t.Errorf("retention = %v, want 48h", got.RetentionOverride)
	}
}
