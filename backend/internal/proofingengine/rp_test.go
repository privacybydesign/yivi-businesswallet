package proofingengine

import (
	"context"
	"encoding/json"
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

func TestSessionTTLBoundsRefused(t *testing.T) {
	s := &Server{}
	for _, seconds := range []int{-1, maxSessionTTLSeconds + 1, 1 << 62} {
		spec := pp.FlowSpec{Name: "x", Steps: []string{"document_capture"}, SessionTTLSeconds: seconds}
		var refused *pp.RejectedError
		if _, err := s.CreateFlow(context.Background(), pp.Tenant{ID: "org"}, spec); !errors.As(err, &refused) || refused.Status != http.StatusBadRequest {
			t.Errorf("CreateFlow(sessionTtlSeconds %d) = %v, want 400", seconds, err)
		}
	}
}

func TestSessionTTLRoundTrips(t *testing.T) {
	spec := pp.FlowSpec{Name: "x", Steps: []string{"document_capture", "nfc_read"}, SessionTTLSeconds: 300}
	fd := toDefinition("org", "f1", spec)
	if fd.SessionTTL != 5*time.Minute {
		t.Errorf("definition's session TTL = %v, want 5m0s", fd.SessionTTL)
	}
	if got := toFlow(fd).SessionTTLSeconds; got != 300 {
		t.Errorf("flow's sessionTtlSeconds = %d, want 300", got)
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

// A version keeps its reasons through the stored definition, and one stored
// before reasons existed reads back without any rather than failing.
func TestAttributeReasonsRoundTrip(t *testing.T) {
	reasons := map[string]map[string]string{"dg1": {"en": "To check your age.", "nl": "Om je leeftijd te controleren."}}
	spec := pp.FlowSpec{
		Name: "x", Steps: []string{"document_capture", "nfc_read"},
		RequestedAttributes: []string{"dg1"}, AttributeReasons: reasons,
	}

	stored, err := json.Marshal(toDefinition("org", "f1", spec))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var fd flow.FlowDefinition
	if err := json.Unmarshal(stored, &fd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := toFlow(fd).AttributeReasons["dg1"]["nl"]; got != reasons["dg1"]["nl"] {
		t.Errorf("stored Dutch reason = %q, want %q", got, reasons["dg1"]["nl"])
	}

	var legacy flow.FlowDefinition
	if err := json.Unmarshal([]byte(`{"ID":"f0","Name":"old","Steps":["document_capture"],"RequestedAttributes":["dg1"]}`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if got := toFlow(legacy).AttributeReasons; len(got) != 0 {
		t.Errorf("a version stored without reasons reads back %v, want none", got)
	}
}
