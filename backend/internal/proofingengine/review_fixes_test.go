package proofingengine

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regula"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
	pp "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/ratelimit"
)

const testDeviceToken = "device-token"

// deviceSession is an in-progress session on flow "flow" with an active
// native device holding testDeviceToken.
func deviceSession(id string) session.Session {
	now := time.Now().UTC()
	sess := inProgress(id)
	sess.Flow, sess.FlowVersion, sess.ExpiresAt = "flow", 1, now.Add(time.Hour)
	sess.Access.Native = &session.DeviceAccess{
		ID: "native", TokenHash: session.HashAccessToken(testDeviceToken), State: session.DeviceStateActive,
		ClaimedAt: now, LastActiveAt: now,
	}
	return sess
}

func appRequest(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(deviceTokenHeader, testDeviceToken)
	return req
}

// A waiting /events poll unseals its session only when the row was written:
// a quiet wait costs a write-time read per tick, not a decrypt of everything
// the session collected.
func TestEventsPollUnsealsOnlyOnChange(t *testing.T) {
	sess := deviceSession("ps_1")
	store := newMemSessions(sess)
	cfg := DefaultConfig()
	cfg.SessionEventsPoll, cfg.SessionEventsWait = 5*time.Millisecond, 100*time.Millisecond
	s := New(cfg, store, singleFlow{fd: flow.FlowDefinition{ID: "flow", Version: 1, Name: "Chip", Steps: []flow.Step{flow.StepDocumentCapture, flow.StepNFCRead}}}, nil, nil)
	mux := http.NewServeMux()
	s.Register(mux)

	since := s.appSessionChangeKey(sess, time.Now())
	before := store.gets.Load()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, appRequest(http.MethodGet, "/app/"+sess.Token+"/events?since="+since, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("events = %d %s", rec.Code, rec.Body.String())
	}
	// About 20 ticks passed; the only unseal allowed is the final view's.
	if got := store.gets.Load() - before; got > 1 {
		t.Errorf("a quiet wait unsealed the session %d times, want at most once", got)
	}
}

// failingRegula is Regula failing on every call.
type failingRegula struct{ facelessRegula }

func (*failingRegula) GetLiveness(context.Context, string) (regula.LivenessTransaction, error) {
	return regula.LivenessTransaction{}, errors.New("regula: liveness status failed with status 502")
}

// A face check that fails through Regula gives its attempt back; one with a
// transaction Regula does not know keeps it spent.
func TestFaceAttemptRefundedOnlyWhenNotTheSubjects(t *testing.T) {
	for name, tc := range map[string]struct {
		regula   RegulaClient
		status   int
		attempts int
	}{
		"Regula fails":        {&failingRegula{}, http.StatusServiceUnavailable, 0},
		"unknown transaction": {&facelessRegula{}, http.StatusBadRequest, 1},
	} {
		t.Run(name, func(t *testing.T) {
			sess := deviceSession("ps_1")
			store := newMemSessions(sess)
			cfg := DefaultConfig()
			cfg.Regula = tc.regula
			fd := flow.FlowDefinition{ID: "flow", Version: 1, Name: "Liveness", Steps: []flow.Step{flow.StepLiveness}, FaceProvider: flow.FaceProviderRegula}
			s := New(cfg, store, singleFlow{fd: fd}, nil, nil)
			mux := http.NewServeMux()
			s.Register(mux)

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, appRequest(http.MethodPost, "/app/"+sess.Token+"/steps/selfie", `{"livenessTransactionId":"tx-1"}`))
			if rec.Code != tc.status {
				t.Fatalf("selfie = %d %s, want %d", rec.Code, rec.Body.String(), tc.status)
			}
			stored, _ := store.Get(sess.TenantID, sess.ID)
			if stored.FaceAttempts != tc.attempts {
				t.Errorf("attempts reserved = %d, want %d", stored.FaceAttempts, tc.attempts)
			}
		})
	}
}

// The engine refuses the Yivi method on a flow it cannot serve, whatever the
// wallet offers: one requiring more than low, one matching a reference photo,
// one taking a document photo.
func TestYiviRefusedOnFlowsItCannotServe(t *testing.T) {
	for name, fd := range map[string]flow.FlowDefinition{
		"requires substantial": {Name: "Chip", Steps: []flow.Step{flow.StepDocumentCapture, flow.StepNFCRead}, RequiredAssuranceLevel: flow.AssuranceLevelSubstantial},
		"reference photo":      {Name: "Face", Steps: []flow.Step{flow.StepFaceVerification}},
		"document photo":       {Name: "Photo", Steps: []flow.Step{flow.StepDocumentPhoto}},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Regula = &facelessRegula{}
			fd.ID, fd.Version = "flow", 1
			s := New(cfg, newMemSessions(), singleFlow{fd: fd}, nil, nil)
			_, err := s.CreateSession(context.Background(), pp.Tenant{ID: "org"}, pp.SessionInput{FlowID: "flow", Method: pp.MethodYivi})
			var refused *pp.RejectedError
			if !errors.As(err, &refused) || refused.Status != http.StatusBadRequest {
				t.Errorf("CreateSession(Yivi) = %v, want 400", err)
			}
		})
	}
}

// An unknown method is the caller's mistake, a 400, not an internal error.
func TestUnknownMethodRefused(t *testing.T) {
	s := New(DefaultConfig(), newMemSessions(), singleFlow{}, nil, nil)
	_, err := s.CreateSession(context.Background(), pp.Tenant{ID: "org"}, pp.SessionInput{FlowID: "flow", Method: "carrier_pigeon"})
	var refused *pp.RejectedError
	if !errors.As(err, &refused) || refused.Status != http.StatusBadRequest {
		t.Errorf("CreateSession(unknown method) = %v, want 400", err)
	}
}

// A reference photo that does not decode is refused when the session is
// made, not at the subject's face step; a readable one is taken.
func TestReferencePhotoMustDecode(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	fd := flow.FlowDefinition{ID: "flow", Version: 1, Name: "Face", Steps: []flow.Step{flow.StepFaceVerification}}
	for name, tc := range map[string]struct {
		data string
		ok   bool
	}{
		"readable":     {base64.StdEncoding.EncodeToString(buf.Bytes()), true},
		"not an image": {base64.StdEncoding.EncodeToString([]byte("not an image")), false},
	} {
		t.Run(name, func(t *testing.T) {
			s := New(DefaultConfig(), newMemSessions(), singleFlow{fd: fd}, nil, nil)
			_, err := s.CreateSession(context.Background(), pp.Tenant{ID: "org"}, pp.SessionInput{
				FlowID: "flow", ReferencePhoto: &pp.Image{Base64: tc.data, MimeType: "image/png"},
			})
			var refused *pp.RejectedError
			if tc.ok && err != nil {
				t.Errorf("CreateSession = %v, want the session made", err)
			}
			if !tc.ok && (!errors.As(err, &refused) || refused.Status != http.StatusBadRequest) {
				t.Errorf("CreateSession = %v, want 400", err)
			}
		})
	}
}

// Deleting a session that is gone is fine; one that is there under another
// token is not erased, and says so.
func TestDeleteSessionWithAnotherToken(t *testing.T) {
	sess := inProgress("ps_1")
	store := newMemSessions(sess)
	s := New(DefaultConfig(), store, nil, nil, nil)
	if err := s.DeleteSession(context.Background(), pp.Tenant{ID: "org"}, sess.ID, "not-its-token"); !errors.Is(err, errWrongSessionToken) {
		t.Errorf("DeleteSession(wrong token) = %v, want errWrongSessionToken", err)
	}
	if _, err := store.Get("org", sess.ID); err != nil {
		t.Errorf("session gone after a refused delete: %v", err)
	}
	if err := s.DeleteSession(context.Background(), pp.Tenant{ID: "org"}, "ps_gone", "any"); err != nil {
		t.Errorf("DeleteSession(gone) = %v, want nil", err)
	}
}

// A review decision keeps who decided and why with the session.
func TestDecideReviewKeepsTheReview(t *testing.T) {
	sess := inProgress("ps_1")
	sess.Status = session.StatusNeedsReview
	store := newMemSessions(sess)
	s := New(DefaultConfig(), store, nil, nil, nil)
	err := s.DecideReview(context.Background(), pp.Tenant{ID: "org"}, sess.ID, sess.Token,
		pp.ReviewDecision{Approve: true, Reason: "Checked the document in person", Reviewer: "Anna Jansen"})
	if err != nil {
		t.Fatalf("DecideReview: %v", err)
	}
	stored, _ := store.Get("org", sess.ID)
	if r := stored.Review; r == nil || r.Reviewer != "Anna Jansen" || r.Reason != "Checked the document in person" || r.DecidedAt.IsZero() {
		t.Errorf("review = %+v, want who decided, why and when", stored.Review)
	}
}

// A Yivi frame is held to the flow's face.match threshold when that is above
// the deployment's, as the Idem path is.
func TestBoundLoginThreshold(t *testing.T) {
	s := New(DefaultConfig(), newMemSessions(), nil, nil, nil)
	stricter := &flow.FlowDefinition{CheckThresholds: map[flow.Check]float64{flow.CheckFaceMatch: 0.9}}
	looser := &flow.FlowDefinition{CheckThresholds: map[flow.Check]float64{flow.CheckFaceMatch: 0.5}}
	for name, tc := range map[string]struct {
		fd   *flow.FlowDefinition
		want float64
	}{
		"no flow":    {nil, regula.DefaultMatchThreshold},
		"stricter":   {stricter, 0.9},
		"looser":     {looser, regula.DefaultMatchThreshold},
		"no setting": {&flow.FlowDefinition{}, regula.DefaultMatchThreshold},
	} {
		if got := s.boundLoginThreshold(tc.fd); got != tc.want {
			t.Errorf("%s: threshold = %v, want %v", name, got, tc.want)
		}
	}
}

// A Yivi session is eIDAS low and not scored on the tier ladder: one check
// out of one would name the top tier.
func TestBoundLoginAssuranceUnscored(t *testing.T) {
	if a := boundLoginAssurance(); a.Level != "" || a.EIDASLevel != flow.AssuranceLevelLow {
		t.Errorf("assurance = %+v, want eIDAS low and no tier", a)
	}
}

// A device mints a handover for its own slot at will, but for the other slot
// only while that is free or its device is away.
func TestMintHandoverForTheOtherSlot(t *testing.T) {
	now := time.Now().UTC()
	for name, tc := range map[string]struct {
		web    *session.DeviceAccess
		status int
	}{
		"other slot free":   {nil, http.StatusOK},
		"other slot active": {&session.DeviceAccess{ID: "web", TokenHash: "x", State: session.DeviceStateActive, ClaimedAt: now, LastActiveAt: now}, http.StatusConflict},
		"other slot away":   {&session.DeviceAccess{ID: "web", TokenHash: "x", State: session.DeviceStateInactive, ClaimedAt: now, LastActiveAt: now}, http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			sess := deviceSession("ps_1")
			sess.Access.Web = tc.web
			s := New(DefaultConfig(), newMemSessions(sess), singleFlow{fd: flow.FlowDefinition{ID: "flow", Version: 1, Name: "Chip", Steps: []flow.Step{flow.StepDocumentCapture, flow.StepNFCRead}}}, nil, nil)
			mux := http.NewServeMux()
			s.Register(mux)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, appRequest(http.MethodPost, "/app/"+sess.Token+"/handover", `{"role":"web"}`))
			if rec.Code != tc.status {
				t.Errorf("mint for the web slot = %d %s, want %d", rec.Code, rec.Body.String(), tc.status)
			}
		})
	}
}

// Minting is rate-limited per session, as claiming is: each mint is audited.
func TestMintHandoverLimited(t *testing.T) {
	sess := deviceSession("ps_1")
	cfg := DefaultConfig()
	cfg.HandoverLimit = ratelimit.Limit{Burst: 2, Per: time.Hour}
	s := New(cfg, newMemSessions(sess), singleFlow{fd: flow.FlowDefinition{ID: "flow", Version: 1, Name: "Chip", Steps: []flow.Step{flow.StepDocumentCapture, flow.StepNFCRead}}}, nil, nil)
	mux := http.NewServeMux()
	s.Register(mux)
	codes := []int{}
	for range 3 {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, appRequest(http.MethodPost, "/app/"+sess.Token+"/handover", ""))
		codes = append(codes, rec.Code)
	}
	if codes[0] != http.StatusOK || codes[1] != http.StatusOK || codes[2] != http.StatusTooManyRequests {
		t.Errorf("three mints = %v, want two OK and then 429", codes)
	}
}

// The chip's DG11 name is stored with the document and is what the finish
// shows, so a name the MRZ cut off reaches the result whole.
func TestFullNameSurvivesToTheFinish(t *testing.T) {
	read := documentInfo{
		Type: "I", IssuingState: "NLD", FirstName: "JOHA", LastName: "VAN DEN BERG SCHOONHOVEN",
		FullFirstName: "Johanna Wilhelmina", FullLastName: "van den Berg-Schoonhoven",
		DateOfBirth: "1990-01-02", DateOfExpiry: "2031-05-01",
	}
	stored := documentEvidence(&read)
	finished := documentInfoFromParsedMRZ(stored.Parsed, time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC))
	if finished.FullFirstName != read.FullFirstName || finished.FullLastName != read.FullLastName {
		t.Errorf("full name at the finish = %q %q, want DG11's", finished.FullFirstName, finished.FullLastName)
	}
	if want := "Johanna Wilhelmina van den Berg-Schoonhoven"; finished.DisplayName != want {
		t.Errorf("display name = %q, want %q", finished.DisplayName, want)
	}
	without := documentInfoFromParsedMRZ(documentEvidence(&documentInfo{FirstName: "ANNA", LastName: "JANSEN"}).Parsed, time.Now())
	if without.DisplayName != "ANNA JANSEN" {
		t.Errorf("display name without DG11 = %q, want the MRZ's", without.DisplayName)
	}
}
