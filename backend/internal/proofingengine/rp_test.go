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

func TestAlwaysReviewRoundTrips(t *testing.T) {
	spec := pp.FlowSpec{Name: "x", Steps: []string{"document_capture", "nfc_read"}, AlwaysReview: true}
	fd := toDefinition("org", "f1", spec)
	if !fd.AlwaysReview || !toFlow(fd).AlwaysReview {
		t.Errorf("always review lost: definition %v, flow %v", fd.AlwaysReview, toFlow(fd).AlwaysReview)
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

// A session created pending has no app link until its app is chosen; the
// choice is made once, and only for an app the flow fits.
func TestChooseMethod(t *testing.T) {
	t.Run("created pending", testChooseMethodCreatedPending)
	t.Run("idem gives a claim once", testChooseMethodIdemClaimsOnce)
	t.Run("yivi sets the method without a claim", testChooseMethodYiviNoClaim)
	t.Run("yivi refused where it does not fit", testChooseMethodYiviRefused)
	t.Run("other methods refused", testChooseMethodOtherMethods)
	t.Run("closed sessions refused", testChooseMethodClosedSessions)
}

var choiceFlow = flow.FlowDefinition{ID: "flow", Version: 1, Name: "Chip", Steps: []flow.Step{flow.StepDocumentCapture, flow.StepNFCRead}}

// pendingChoice is a session created with its app still to choose.
func pendingChoice(id string) session.Session {
	return session.Session{
		ID: id, TenantID: "org", Token: "tok-" + id, Status: session.StatusCreated,
		Method: session.MethodPending, Flow: "flow", FlowVersion: 1, ExpiresAt: time.Now().Add(time.Hour),
	}
}

// refusedStatus is err's HTTP status when the engine refused, else 0.
func refusedStatus(err error) int {
	var refused *pp.RejectedError
	if !errors.As(err, &refused) {
		return 0
	}
	return refused.Status
}

func testChooseMethodCreatedPending(t *testing.T) {
	ctx, tenant := context.Background(), pp.Tenant{ID: "org"}
	store := newMemSessions()
	s := New(DefaultConfig(), store, singleFlow{fd: choiceFlow}, nil, nil)
	created, err := s.CreateSession(ctx, tenant, pp.SessionInput{FlowID: "flow", Method: pp.MethodPending})
	if err != nil || created.Claim != nil {
		t.Fatalf("CreateSession(pending) = %+v, %v; want no claim", created, err)
	}
	if stored, _ := store.Get("org", created.ID); stored.Method != session.MethodPending {
		t.Errorf("stored method = %q, want %q", stored.Method, session.MethodPending)
	}
}

func testChooseMethodIdemClaimsOnce(t *testing.T) {
	ctx, tenant := context.Background(), pp.Tenant{ID: "org"}
	sess := pendingChoice("ps_idem")
	store := newMemSessions(sess)
	s := New(DefaultConfig(), store, singleFlow{fd: choiceFlow}, nil, nil)
	chosen, err := s.ChooseMethod(ctx, tenant, sess.ID, sess.Token, pp.MethodIdem)
	if err != nil || chosen.Claim == nil || chosen.Claim.DeepLink == "" {
		t.Fatalf("ChooseMethod(idem) = %+v, %v; want a claim", chosen, err)
	}
	if stored, _ := store.Get("org", sess.ID); stored.Method != session.MethodNFCPassport {
		t.Errorf("stored method = %q, want %q", stored.Method, session.MethodNFCPassport)
	}
	for _, m := range []pp.Method{pp.MethodIdem, pp.MethodYivi} {
		if _, err := s.ChooseMethod(ctx, tenant, sess.ID, sess.Token, m); refusedStatus(err) != http.StatusConflict {
			t.Errorf("ChooseMethod(%s) again = %v, want 409", m, err)
		}
	}
}

func testChooseMethodYiviNoClaim(t *testing.T) {
	ctx, tenant := context.Background(), pp.Tenant{ID: "org"}
	sess := pendingChoice("ps_yivi")
	store := newMemSessions(sess)
	s := New(DefaultConfig(), store, singleFlow{fd: choiceFlow}, nil, nil)
	chosen, err := s.ChooseMethod(ctx, tenant, sess.ID, sess.Token, pp.MethodYivi)
	if err != nil || chosen.Claim != nil {
		t.Fatalf("ChooseMethod(yivi) = %+v, %v; want no claim", chosen, err)
	}
	if stored, _ := store.Get("org", sess.ID); stored.Method != session.MethodBiometricBoundLogin {
		t.Errorf("stored method = %q, want %q", stored.Method, session.MethodBiometricBoundLogin)
	}
}

func testChooseMethodYiviRefused(t *testing.T) {
	ctx, tenant := context.Background(), pp.Tenant{ID: "org"}
	sess := pendingChoice("ps_substantial")
	substantial := choiceFlow
	substantial.RequiredAssuranceLevel = flow.AssuranceLevelSubstantial
	store := newMemSessions(sess)
	s := New(DefaultConfig(), store, singleFlow{fd: substantial}, nil, nil)
	if _, err := s.ChooseMethod(ctx, tenant, sess.ID, sess.Token, pp.MethodYivi); refusedStatus(err) != http.StatusBadRequest {
		t.Errorf("ChooseMethod(yivi) on a substantial flow = %v, want 400", err)
	}
	if stored, _ := store.Get("org", sess.ID); stored.Method != session.MethodPending {
		t.Errorf("stored method = %q, want still pending", stored.Method)
	}
}

func testChooseMethodOtherMethods(t *testing.T) {
	ctx, tenant := context.Background(), pp.Tenant{ID: "org"}
	sess := pendingChoice("ps_other")
	s := New(DefaultConfig(), newMemSessions(sess), singleFlow{fd: choiceFlow}, nil, nil)
	for _, m := range []pp.Method{pp.MethodBrowser, pp.MethodPending, "", "carrier_pigeon"} {
		if _, err := s.ChooseMethod(ctx, tenant, sess.ID, sess.Token, m); refusedStatus(err) != http.StatusBadRequest {
			t.Errorf("ChooseMethod(%q) = %v, want 400", m, err)
		}
	}
}

func testChooseMethodClosedSessions(t *testing.T) {
	ctx, tenant := context.Background(), pp.Tenant{ID: "org"}
	expired, done, chosen := pendingChoice("ps_expired"), pendingChoice("ps_done"), pendingChoice("ps_chosen")
	expired.Status, done.Status, chosen.Method = session.StatusExpired, session.StatusApproved, session.MethodNFCPassport
	s := New(DefaultConfig(), newMemSessions(expired, done, chosen), singleFlow{fd: choiceFlow}, nil, nil)
	for _, tc := range []struct {
		sess session.Session
		want int
	}{{expired, http.StatusGone}, {done, http.StatusConflict}, {chosen, http.StatusConflict}} {
		if _, err := s.ChooseMethod(ctx, tenant, tc.sess.ID, tc.sess.Token, pp.MethodIdem); refusedStatus(err) != tc.want {
			t.Errorf("ChooseMethod on %s = %v, want %d", tc.sess.ID, err, tc.want)
		}
	}
}
