//go:build integration

package proofingengine_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gmrtd/gmrtd/document"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/mrtdverify/mrtdtestfixtures"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regula"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
	pp "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

const (
	testKey     = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	testBaseURL = "http://wallet.test"
	orgName     = "Acme BV"
)

type orgNames struct{}

func (orgNames) DisplayName(context.Context, string) (string, error) { return orgName, nil }

type harness struct {
	engine   *proofingengine.Engine
	sessions *session.PostgresStore
	flows    *flow.PostgresStore
	tenant   pp.Tenant
	mux      *http.ServeMux
	changed  chan string
}

// fakeRegula matches every frame that is not "noface" with similarity.
type fakeRegula struct{ similarity float64 }

func (fakeRegula) GetLiveness(context.Context, string) (regula.LivenessTransaction, error) {
	return regula.LivenessTransaction{}, regula.ErrTransactionNotFound
}

func (fakeRegula) Match(context.Context, string, string) (regula.MatchResult, error) {
	return regula.MatchResult{}, nil
}

func (f fakeRegula) MatchImages(_ context.Context, _, live string) (regula.ImageMatch, error) {
	if live == base64.StdEncoding.EncodeToString([]byte("noface")) {
		return regula.ImageMatch{}, nil
	}
	return regula.ImageMatch{LiveFaceDetected: true, Similarity: f.similarity}, nil
}

func (fakeRegula) DeleteLiveness(context.Context, string) error { return nil }

func newHarness(t *testing.T, configure ...func(*proofingengine.Config)) harness {
	t.Helper()
	pool, _ := testdb.Fresh(t)
	var orgID string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ($1, 'acme', 'kvk-acme', 'NL.KVK.acme', 'acme@qerds.localhost') RETURNING id::text`, orgName,
	).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	cipher, err := crypto.NewCipher(testKey)
	if err != nil {
		t.Fatal(err)
	}
	cfg := proofingengine.DefaultConfig()
	cfg.PublicBaseURL = testBaseURL
	for _, c := range configure {
		c(&cfg)
	}
	h := harness{tenant: pp.Tenant{ID: orgID}, mux: http.NewServeMux(), changed: make(chan string, 64)}
	h.sessions, h.flows = session.NewPostgresStore(pool, cipher), flow.NewPostgresStore(pool)
	h.engine = proofingengine.New(cfg, h.sessions, h.flows, orgNames{},
		func(_ context.Context, id string) { h.changed <- id })
	h.engine.Register(h.mux)
	go h.engine.Run(t.Context())
	return h
}

func (h harness) do(t *testing.T, method, path, deviceToken string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	if deviceToken != "" {
		req.Header.Set("X-Device-Token", deviceToken)
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// waitChanged waits for the engine to report sessionID as changed.
func (h harness) waitChanged(t *testing.T, sessionID string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case id := <-h.changed:
			if id == sessionID {
				return
			}
		case <-deadline:
			t.Fatalf("no change reported for %s", sessionID)
		}
	}
}

// chipPortrait is the JPEG2000 portrait inside the fixture's DG2, base64, as
// the app sends it with the chip read.
func chipPortrait(t *testing.T) string {
	t.Helper()
	raw, err := hex.DecodeString(mrtdtestfixtures.Dg2Hex)
	if err != nil {
		t.Fatal(err)
	}
	dg2, err := document.NewDG2(raw)
	if err != nil || dg2 == nil || len(dg2.Images) == 0 {
		t.Fatalf("fixture DG2 has no portrait: %v", err)
	}
	return base64.StdEncoding.EncodeToString(dg2.Images[0].Image)
}

var chipFlow = pp.FlowSpec{
	Name: "Passport", Steps: []string{"document_capture", "nfc_read"},
	RequiredChecks: []string{"nfc.passive_auth"},
}

// The Idem app's whole run, as the vcmrtd client drives it: claim the
// mailed link, scan the MRZ, read the chip, submit; the wallet then reads
// the outcome and the identity.
func TestIdemAppRunsAFlowToItsOutcome(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	f, err := h.engine.CreateFlow(ctx, h.tenant, chipFlow)
	if err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}
	sess, err := h.engine.CreateSession(ctx, h.tenant, pp.SessionInput{FlowID: f.ID, ClientReference: "req-1", Language: "nl"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.Claim == nil || sess.FlowVersion != 1 {
		t.Fatalf("session = %+v; want a claim on version 1", sess)
	}
	link, err := url.Parse(sess.Claim.DeepLink)
	if err != nil || link.Scheme != "vcmrtd" || link.Query().Get("api") != testBaseURL {
		t.Fatalf("deep link = %q; want vcmrtd with api=%s", sess.Claim.DeepLink, testBaseURL)
	}

	code, claim := h.do(t, http.MethodPost, "/app/handover/"+link.Query().Get("handover")+"/claim", "", nil)
	if code != http.StatusOK {
		t.Fatalf("claim = %d %v", code, claim)
	}
	token, device := claim["token"].(string), claim["deviceToken"].(string)
	view := claim["session"].(map[string]any)
	if view["relyingParty"] != orgName || view["currentStep"] != "document_capture" || view["language"] != "nl" {
		t.Errorf("app view = %v; want the org's name, document_capture first, in Dutch", view)
	}
	h.waitChanged(t, sess.ID)
	if res, err := h.engine.SessionStatus(ctx, h.tenant, sess.ID, sess.Token); err != nil || res.App != pp.AppConnected || res.Status != pp.StatusOpened {
		t.Errorf("status = %+v, %v; want opened with the app connected", res, err)
	}
	if code, _ := h.do(t, http.MethodPost, "/app/handover/"+link.Query().Get("handover")+"/claim", "", nil); code != http.StatusConflict {
		t.Errorf("second claim = %d, want 409: a claim is single use", code)
	}
	if code, _ := h.do(t, http.MethodGet, "/app/"+token, "", nil); code != http.StatusUnauthorized {
		t.Errorf("read without device token = %d, want 401", code)
	}

	doc := map[string]any{"type": "P", "number": "X1234567", "issuingState": "NLD", "firstName": "Anna", "lastName": "Eriksson"}
	code, step := h.do(t, http.MethodPost, "/app/"+token+"/steps/document_capture", device, map[string]any{
		"document": doc,
		"chipAccess": map[string]any{
			"documentType": "passport", "documentNumber": "X1234567", "countryCode": "NLD",
			"dateOfBirth": "1990-01-01", "dateOfExpiry": "2030-01-01",
		},
	})
	if code != http.StatusOK || step["currentStep"] != "nfc_read" {
		t.Fatalf("document_capture = %d %v; want nfc_read next", code, step)
	}
	code, step = h.do(t, http.MethodPost, "/app/"+token+"/steps/nfc", device, map[string]any{
		"document": doc,
		"photo":    map[string]any{"imageBase64": chipPortrait(t), "mimeType": "image/jp2"},
		"mrtdEvidence": map[string]any{
			"efSod":      mrtdtestfixtures.TestSodHex,
			"dataGroups": map[string]string{"DG1": mrtdtestfixtures.TestDg1Hex, "DG2": mrtdtestfixtures.Dg2Hex},
		},
	})
	if code != http.StatusOK || step["readyToSubmit"] != true {
		t.Fatalf("nfc = %d %v; want ready to submit", code, step)
	}
	code, step = h.do(t, http.MethodPost, "/app/"+token+"/submit", device, nil)
	if code != http.StatusOK || step["lifecycle"] != "COMPLETE" {
		t.Fatalf("submit = %d %v; want the session complete", code, step)
	}

	res, err := h.engine.SessionResult(ctx, h.tenant, sess.ID, sess.Token)
	if err != nil {
		t.Fatalf("SessionResult: %v", err)
	}
	if res.Status != pp.StatusApproved || res.Method != pp.MethodIdem || res.CompletedAt == nil {
		t.Errorf("result = %+v; want approved from the Idem app", res)
	}
	id, err := h.engine.SessionIdentity(ctx, h.tenant, sess.ID, sess.Token)
	if err != nil || id.Evidence == nil || id.Evidence.Type != pp.EvidenceEMRTD || id.Evidence.PassiveAuth == pp.CheckNotPerformed {
		t.Errorf("identity = %+v, %v; want chip evidence with passive authentication run", id, err)
	}
	// The chip's portrait comes back in a type a browser shows (this
	// fixture's DG2 is a JPEG labelled image/jp2).
	if id.Photo == nil || id.Photo.MimeType != "image/jpeg" {
		t.Errorf("photo = %v; want the chip portrait as image/jpeg", id.Photo != nil)
	}

	// A wallet holding the wrong token, or another org, reads nothing.
	if _, err := h.engine.SessionStatus(ctx, h.tenant, sess.ID, "wrong"); !errors.Is(err, pp.ErrNotFound) {
		t.Errorf("wrong token = %v, want ErrNotFound", err)
	}
	if _, err := h.engine.SessionStatus(ctx, pp.Tenant{ID: "00000000-0000-0000-0000-000000000000"}, sess.ID, sess.Token); !errors.Is(err, pp.ErrNotFound) {
		t.Errorf("other org = %v, want ErrNotFound", err)
	}
	if err := h.engine.DeleteSession(ctx, h.tenant, sess.ID, sess.Token); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := h.engine.SessionStatus(ctx, h.tenant, sess.ID, sess.Token); !errors.Is(err, pp.ErrNotFound) {
		t.Errorf("after delete = %v, want ErrNotFound", err)
	}
}

func TestFlowsAreVersionedPerOrg(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.engine.CreateFlow(ctx, h.tenant, pp.FlowSpec{Name: "x", Steps: []string{"nfc_read"}}); err == nil {
		t.Fatal("nfc_read without document_capture must be refused")
	} else if rejected := (*pp.RejectedError)(nil); !errors.As(err, &rejected) || !strings.HasPrefix(rejected.Message, "flow: ") {
		t.Fatalf("invalid flow = %v, want a RejectedError with the rule", err)
	}
	f, err := h.engine.CreateFlow(ctx, h.tenant, chipFlow)
	if err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}
	next := chipFlow
	next.Name = "Passport v2"
	if _, err := h.engine.CreateFlowVersion(ctx, h.tenant, f.ID, next); err != nil {
		t.Fatalf("CreateFlowVersion: %v", err)
	}
	flows, err := h.engine.ListFlows(ctx, h.tenant)
	if err != nil || len(flows) != 1 || flows[0].Version != 2 || flows[0].Name != "Passport v2" {
		t.Fatalf("flows = %+v, %v; want version 2 active", flows, err)
	}
	if back, err := h.engine.ActivateFlowVersion(ctx, h.tenant, f.ID, 1); err != nil || back.Version != 1 || !back.Active {
		t.Fatalf("activate v1 = %+v, %v", back, err)
	}
	versions, err := h.engine.ListFlowVersions(ctx, h.tenant, f.ID)
	if err != nil || len(versions) != 2 || !versions[0].Active || versions[1].Active {
		t.Errorf("versions = %+v, %v; want v1 active again", versions, err)
	}
	if _, err := h.engine.ListFlows(ctx, pp.Tenant{ID: "00000000-0000-0000-0000-000000000000"}); err != nil {
		t.Errorf("another org's flows: %v", err)
	}
	if _, err := h.engine.CreateFlowVersion(ctx, pp.Tenant{ID: "00000000-0000-0000-0000-000000000000"}, f.ID, next); !errors.Is(err, pp.ErrNotFound) {
		t.Errorf("another org's flow version = %v, want ErrNotFound", err)
	}
}

func TestTestModeOnlyRunsScriptedSessions(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	sandbox := pp.Tenant{ID: h.tenant.ID, Sandbox: true}
	if _, err := h.engine.CreateSession(ctx, sandbox, pp.SessionInput{ClientReference: "r"}); err == nil {
		t.Error("a test session without a scripted outcome must be refused")
	}
	if _, err := h.engine.CreateSession(ctx, h.tenant, pp.SessionInput{ScriptedOutcome: "approve"}); err == nil {
		t.Error("a live session must refuse a scripted outcome")
	}
	sess, err := h.engine.CreateSession(ctx, sandbox, pp.SessionInput{ClientReference: "r", ScriptedOutcome: "reject:DOC_EXPIRED"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	res, err := h.engine.SessionStatus(ctx, sandbox, sess.ID, sess.Token)
	if err != nil || res.Status != pp.StatusRejected || res.ErrorCode != "DOC_EXPIRED" {
		t.Errorf("scripted = %+v, %v; want rejected DOC_EXPIRED", res, err)
	}
}

func TestReviewAndCancel(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	sandbox := pp.Tenant{ID: h.tenant.ID, Sandbox: true}
	sess, err := h.engine.CreateSession(ctx, sandbox, pp.SessionInput{ScriptedOutcome: "needs_review"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.engine.DecideReview(ctx, sandbox, sess.ID, sess.Token, pp.ReviewDecision{Reason: "ok", Reviewer: "sam"}); err != nil {
		t.Fatalf("DecideReview: %v", err)
	}
	res, err := h.engine.SessionStatus(ctx, sandbox, sess.ID, sess.Token)
	if err != nil || res.Status != pp.StatusRejected || res.ErrorCode != "MANUAL_REVIEW_REJECTED" {
		t.Errorf("after review = %+v, %v", res, err)
	}
	var rejected *pp.RejectedError
	if err := h.engine.CancelSession(ctx, sandbox, sess.ID, sess.Token); !errors.As(err, &rejected) || rejected.Status != http.StatusConflict {
		t.Errorf("cancel a decided session = %v, want 409", err)
	}

	f, err := h.engine.CreateFlow(ctx, h.tenant, chipFlow)
	if err != nil {
		t.Fatal(err)
	}
	live, err := h.engine.CreateSession(ctx, h.tenant, pp.SessionInput{FlowID: f.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.engine.CancelSession(ctx, h.tenant, live.ID, live.Token); err != nil {
		t.Fatalf("CancelSession: %v", err)
	}
	h.waitChanged(t, live.ID)
	if res, err := h.engine.SessionStatus(ctx, h.tenant, live.ID, live.Token); err != nil || res.Status != pp.StatusCancelled {
		t.Errorf("after cancel = %+v, %v", res, err)
	}
	link, _ := url.Parse(live.Claim.DeepLink)
	if code, _ := h.do(t, http.MethodPost, "/app/handover/"+link.Query().Get("handover")+"/claim", "", nil); code == http.StatusOK {
		t.Error("a cancelled session's link must not claim")
	}
}

func TestYiviNeedsRegula(t *testing.T) {
	h := newHarness(t)
	if _, err := h.engine.CreateSession(context.Background(), h.tenant, pp.SessionInput{Method: pp.MethodYivi}); !errors.Is(err, pp.ErrMethodUnavailable) {
		t.Errorf("Yivi without Regula = %v, want ErrMethodUnavailable", err)
	}
}

// The Yivi method's face check lives in the session, not in one replica's
// memory: a second engine on the same database scores the next frames.
func TestYiviFaceCheckRunsAcrossReplicas(t *testing.T) {
	withRegula := func(c *proofingengine.Config) { c.Regula = fakeRegula{similarity: 0.9} }
	h := newHarness(t, withRegula)
	ctx := context.Background()
	f, err := h.engine.CreateFlow(ctx, h.tenant, pp.FlowSpec{
		Name: "Yivi", Steps: []string{"document_capture", "nfc_read", "face_verification"},
		FaceProvider: "regula", SelfieLocation: "native", RequiredChecks: []string{"nfc.passive_auth", "face.match"},
	})
	if err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}
	sess, err := h.engine.CreateSession(ctx, h.tenant, pp.SessionInput{FlowID: f.ID, Method: pp.MethodYivi})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	photo := base64.StdEncoding.EncodeToString([]byte("not really a jpeg"))
	got, err := h.engine.SubmitReference(ctx, h.tenant, sess.ID, sess.Token, pp.Reference{
		Credential: "pbdf-staging.pbdf.passport", Photo: photo, Attributes: map[string]string{"firstName": "Anna"},
	})
	if err != nil || !got.OK || got.StableFrames != 3 {
		t.Fatalf("SubmitReference = %+v, %v", got, err)
	}
	otherCfg := proofingengine.DefaultConfig()
	withRegula(&otherCfg)
	other := proofingengine.New(otherCfg, h.sessions, h.flows, orgNames{}, nil)

	frame := func(e *proofingengine.Engine, s string) pp.FaceVerdict {
		t.Helper()
		v, err := e.SubmitFaceFrame(ctx, sess.Token, base64.StdEncoding.EncodeToString([]byte(s)))
		if err != nil {
			t.Fatalf("frame %q: %v", s, err)
		}
		return v
	}
	if v := frame(h.engine, "a"); v.Consecutive != 1 || v.Decision != pp.FaceDecisionPending {
		t.Fatalf("first frame = %+v", v)
	}
	if v := frame(other, "a"); v.Consecutive != 0 {
		t.Errorf("a replayed frame = %+v, want the run broken", v)
	}
	frame(other, "b")
	frame(h.engine, "c")
	if v := frame(other, "d"); v.Decision != pp.FaceDecisionApproved {
		t.Fatalf("after three matches = %+v, want approved", v)
	}
	res, err := h.engine.SessionResult(ctx, h.tenant, sess.ID, sess.Token)
	if err != nil || res.Status != pp.StatusApproved || res.Method != pp.MethodYivi || res.Name != "Anna" {
		t.Errorf("result = %+v, %v; want approved from the Yivi app", res, err)
	}
}

// A face check without the chip read matches the live face against the
// relying party's own photo: required on such a flow, refused on a chip
// flow, handed to the app for the face step, and dropped once the session
// can no longer run it.
func TestReferencePhotoFaceCheck(t *testing.T) {
	h := newHarness(t, func(c *proofingengine.Config) { c.Regula = fakeRegula{similarity: 0.9} })
	ctx := context.Background()
	faceOnly, err := h.engine.CreateFlow(ctx, h.tenant, pp.FlowSpec{
		Name: "Face only", Steps: []string{"face_verification"}, FaceProvider: "regula", SelfieLocation: "native",
		RequiredChecks: []string{"face.match", "face.liveness"},
	})
	if err != nil {
		t.Fatalf("CreateFlow(face only): %v", err)
	}
	chip, err := h.engine.CreateFlow(ctx, h.tenant, chipFlow)
	if err != nil {
		t.Fatalf("CreateFlow(chip): %v", err)
	}
	photo := &pp.Image{MimeType: "image/jpeg", Base64: base64.StdEncoding.EncodeToString([]byte("the org's photo"))}
	var rejected *pp.RejectedError
	if _, err := h.engine.CreateSession(ctx, h.tenant, pp.SessionInput{FlowID: faceOnly.ID}); !errors.As(err, &rejected) {
		t.Errorf("face-only session without a photo = %v, want refused", err)
	}
	if _, err := h.engine.CreateSession(ctx, h.tenant, pp.SessionInput{FlowID: chip.ID, ReferencePhoto: photo}); !errors.As(err, &rejected) {
		t.Errorf("chip session with a photo = %v, want refused", err)
	}
	bad := &pp.Image{MimeType: "image/svg+xml", Base64: photo.Base64}
	if _, err := h.engine.CreateSession(ctx, h.tenant, pp.SessionInput{FlowID: faceOnly.ID, ReferencePhoto: bad}); !errors.As(err, &rejected) {
		t.Errorf("session with an SVG photo = %v, want refused", err)
	}

	sess, err := h.engine.CreateSession(ctx, h.tenant, pp.SessionInput{FlowID: faceOnly.ID, ReferencePhoto: photo})
	if err != nil || sess.Claim == nil {
		t.Fatalf("CreateSession = %+v, %v; want a claim", sess, err)
	}
	link, err := url.Parse(sess.Claim.DeepLink)
	if err != nil {
		t.Fatal(err)
	}
	code, claim := h.do(t, http.MethodPost, "/app/handover/"+link.Query().Get("handover")+"/claim", "", nil)
	if code != http.StatusOK {
		t.Fatalf("claim = %d %v", code, claim)
	}
	view := claim["session"].(map[string]any)
	ref, _ := view["faceReference"].(map[string]any)
	if view["currentStep"] != "face_verification" || ref["imageBase64"] != photo.Base64 || ref["mimeType"] != photo.MimeType {
		t.Errorf("app view = step %v, faceReference %v; want the face step against the org's photo", view["currentStep"], ref)
	}

	if err := h.engine.CancelSession(ctx, h.tenant, sess.ID, sess.Token); err != nil {
		t.Fatalf("CancelSession: %v", err)
	}
	stored, err := h.sessions.Get(h.tenant.ID, sess.ID)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	if stored.ReferencePhoto != "" || stored.ReferencePhotoMime != "" {
		t.Error("a cancelled session still holds the reference photo")
	}
}
