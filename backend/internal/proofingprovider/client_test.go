package proofingprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	testAdminKey     = "admin-secret"
	testAPIKey       = "sk_live_tenantkey"
	testSessionToken = "rp-bearer-token"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient(srv.URL+"/", testAdminKey, srv.Client()), srv
}

func TestCreateTenantAndKeyUseAdminKey(t *testing.T) {
	var paths []string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if got := r.Header.Get(headerAdminKey); got != testAdminKey {
			t.Errorf("X-Admin-Key = %q, want the admin key", got)
		}
		w.WriteHeader(http.StatusCreated)
		switch r.URL.Path {
		case "/api/v1/admin/tenants":
			var body struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.ID != "t1" || body.Name != "Acme" {
				t.Errorf("tenant request = %+v, want the org's id and name", body)
			}
			_, _ = w.Write([]byte(`{"id":"t1","name":"Acme","webhookSecret":"whsec_x"}`))
		case "/api/v1/admin/tenants/t1/keys":
			var body struct {
				Environment string   `json:"environment"`
				Scopes      []string `json:"scopes"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Environment != string(KeyTest) || len(body.Scopes) != len(TenantKeyScopes) {
				t.Errorf("key request = %+v, want test with the tenant scopes", body)
			}
			_, _ = w.Write([]byte(`{"id":"k1","plaintext":"sk_test_abc"}`))
		case "/api/v1/admin/tenants/t1/webhook-secret":
			_, _ = w.Write([]byte(`{"webhookSecret":"whsec_y"}`))
		}
	})

	tenant, err := client.CreateTenant(context.Background(), "t1", "Acme")
	if err != nil || tenant.ID != "t1" || tenant.WebhookSecret != "whsec_x" {
		t.Fatalf("CreateTenant = %+v, %v", tenant, err)
	}
	key, err := client.CreateAPIKey(context.Background(), tenant.ID, KeyTest, TenantKeyScopes)
	if err != nil || key != "sk_test_abc" {
		t.Fatalf("CreateAPIKey = %q, %v", key, err)
	}
	secret, err := client.RotateWebhookSecret(context.Background(), tenant.ID)
	if err != nil || secret != "whsec_y" {
		t.Fatalf("RotateWebhookSecret = %q, %v", secret, err)
	}
	want := []string{"POST /api/v1/admin/tenants", "POST /api/v1/admin/tenants/t1/keys", "POST /api/v1/admin/tenants/t1/webhook-secret"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %v, want %v", paths, want)
	}
}

func TestCreateTenantTakenIsErrTenantExists(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"a tenant with this id already exists"}`))
	})
	if _, err := client.CreateTenant(context.Background(), "t1", "Acme"); !errors.Is(err, ErrTenantExists) {
		t.Fatalf("CreateTenant = %v, want ErrTenantExists", err)
	}
}

func TestCreateSessionReadsNativeClaim(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(headerAPIKey) != testAPIKey {
			t.Errorf("X-Api-Key missing")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["flow"] != "f1" || body["clientReference"] != "req-1" || body["ttlSeconds"] != float64(900) {
			t.Errorf("session request = %v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"s1","token":"tok","status":"created","expiresAt":"2026-09-23T10:15:00Z","flowVersion":3,
			"claims":{"web":{"url":"https://ips/x"},"native":{"deepLink":"vcmrtd://verify?handover=h","expiresAt":"2026-09-23T10:05:00Z"}}}`))
	})

	sess, err := client.CreateSession(context.Background(), testAPIKey, SessionInput{FlowID: "f1", ClientReference: "req-1", TTL: 15 * time.Minute})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.ID != "s1" || sess.Token != "tok" || sess.Claim == nil || sess.Claim.DeepLink != "vcmrtd://verify?handover=h" || sess.FlowVersion != 3 {
		t.Errorf("session = %+v", sess)
	}
}

func TestFlowVersionCalls(t *testing.T) {
	var calls []string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.EscapedPath())
		if r.Header.Get(headerAPIKey) != testAPIKey {
			t.Errorf("X-Api-Key missing on %s", r.URL.Path)
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[{"id":"f/1","version":1,"active":false},{"id":"f/1","version":2,"active":true}]`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"f/1","version":2,"active":true,"name":"Passport","steps":["nfc_read"],"blurFace":true}`))
	})
	ctx := context.Background()
	created, err := client.CreateFlowVersion(ctx, testAPIKey, "f/1", FlowSpec{Name: "Passport", Steps: []string{"nfc_read"}})
	if err != nil || created.Version != 2 || !created.Active || created.BlurFace == nil || !*created.BlurFace {
		t.Fatalf("CreateFlowVersion = %+v, %v", created, err)
	}
	versions, err := client.ListFlowVersions(ctx, testAPIKey, "f/1")
	if err != nil || len(versions) != 2 {
		t.Fatalf("ListFlowVersions = %+v, %v", versions, err)
	}
	if _, err := client.ActivateFlowVersion(ctx, testAPIKey, "f/1", 1); err != nil {
		t.Fatalf("ActivateFlowVersion: %v", err)
	}
	want := []string{
		"POST /api/v1/flows/f%2F1/versions",
		"GET /api/v1/flows/f%2F1/versions",
		"POST /api/v1/flows/f%2F1/versions/1/activate",
	}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}

func TestSessionResultDecodesAssuranceAndNameOnly(t *testing.T) {
	cases := map[string]struct {
		document string
		want     string
	}{
		"display name wins": {`{"displayName":"Ánna Jansen","firstName":"ANNA","lastName":"JANSEN","personalNumber":"999999990"}`, "Ánna Jansen"},
		"mrz name":          {`{"firstName":"ANNA","lastName":"JANSEN","personalNumber":"999999990"}`, "ANNA JANSEN"},
		"no name":           {`{"personalNumber":"999999990"}`, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"id":"s1","status":"approved","completedAt":"2026-09-23T10:10:00Z",
					"result":{"document":` + tc.document + `,"assurance":{"level":"high","eidasLevel":"substantial"}}}`))
			})
			res, err := client.SessionResult(context.Background(), testAPIKey, "s1", testSessionToken)
			if err != nil {
				t.Fatalf("SessionResult: %v", err)
			}
			if res.Status != StatusApproved || res.AssuranceLevel != "high" || res.EIDASLevel != "substantial" || res.CompletedAt == nil {
				t.Errorf("result = %+v", res)
			}
			if res.Name != tc.want {
				t.Errorf("name = %q, want %q", res.Name, tc.want)
			}
		})
	}
}

// The method is read off the devices that took part and a Yivi disclosure,
// never off anything that identifies the device or the person.
func TestSessionResultDecodesTheMethod(t *testing.T) {
	cases := map[string]struct {
		body string
		want Method
	}{
		"nobody opened it": {`{"status":"created","devices":[]}`, ""},
		"idem app":         {`{"status":"in_progress","devices":[{"deviceId":"d1","role":"native","via":"claim"}]}`, MethodIdem},
		"web then idem": {`{"status":"approved","devices":[{"deviceId":"d1","role":"web","via":"claim"},` +
			`{"deviceId":"d2","role":"native","via":"handover"}]}`, MethodIdem},
		"browser only": {`{"status":"in_progress","devices":[{"deviceId":"d1","role":"web","via":"claim"}]}`, MethodBrowser},
		"yivi":         {`{"status":"approved","result":{"disclosure":{"source":"yivi"}},"devices":[{"role":"web"}]}`, MethodYivi},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			})
			res, err := client.SessionResult(context.Background(), testAPIKey, "s1", testSessionToken)
			if err != nil {
				t.Fatalf("SessionResult: %v", err)
			}
			if res.Method != tc.want {
				t.Errorf("method = %q, want %q", res.Method, tc.want)
			}
		})
	}
}

func TestErrorsMapAndRedact(t *testing.T) {
	status := http.StatusBadRequest
	client, srv := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"face_match requires the face.match check"}`))
	})

	_, err := client.CreateFlow(context.Background(), testAPIKey, FlowSpec{Name: "x"})
	var rejected *RejectedError
	if !errors.As(err, &rejected) || rejected.Message != "face_match requires the face.match check" {
		t.Fatalf("CreateFlow err = %v, want a RejectedError with IPS's message", err)
	}

	status = http.StatusNotFound
	if _, err := client.SessionResult(context.Background(), testAPIKey, "s1", testSessionToken); !errors.Is(err, ErrNotFound) {
		t.Errorf("404 err = %v, want ErrNotFound", err)
	}

	status = http.StatusInternalServerError
	_, err = client.ListFlows(context.Background(), testAPIKey)
	if err == nil {
		t.Fatal("500 answered no error")
	}
	for _, secret := range []string{srv.URL, testAPIKey, testAdminKey} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error %q leaks %q", err, secret)
		}
	}
}

// A Yivi session's reference goes to IPS as the relying party, with the
// session's bearer token, marked as the wallet's OpenID4VP disclosure.
func TestSubmitReferenceSendsTheDisclosure(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/sessions/s1/reference" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get(headerAPIKey) != testAPIKey || r.Header.Get("Authorization") != "Bearer "+testSessionToken {
			t.Errorf("headers = %v, want the API key and session token", r.Header)
		}
		var body struct {
			Source     string            `json:"source"`
			Credential string            `json:"credential"`
			Image      string            `json:"image"`
			Attributes map[string]string `json:"attributes"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Source != referenceSourceOpenID4VP || body.Credential != "pbdf-staging.pbdf.passport" || body.Image != "cGhvdG8=" || body.Attributes["firstName"] != "Anna" {
			t.Errorf("body = %+v", body)
		}
		_, _ = w.Write([]byte(`{"ok":true,"status":"in_progress","stableFrames":3,"maxAttempts":40}`))
	})
	got, err := client.SubmitReference(context.Background(), testAPIKey, "s1", testSessionToken, Reference{
		Credential: "pbdf-staging.pbdf.passport", Photo: "cGhvdG8=", Attributes: map[string]string{"firstName": "Anna"},
	})
	if err != nil || !got.OK || got.StableFrames != 3 || got.MaxAttempts != 40 {
		t.Fatalf("SubmitReference = %+v, %v", got, err)
	}
}

// An IPS that takes no wallet reference answers 503: the method cannot run.
func TestSubmitReferenceUnavailable(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, err := client.SubmitReference(context.Background(), testAPIKey, "s1", testSessionToken, Reference{})
	if !errors.Is(err, ErrMethodUnavailable) {
		t.Fatalf("err = %v, want ErrMethodUnavailable", err)
	}
}

func TestSessionStatusDecodesWithoutPersonalData(t *testing.T) {
	var paths []string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = w.Write([]byte(`{"id":"s1","status":"approved","completedAt":"2026-09-23T10:10:00Z",
			"assurance":{"level":"high","eidasLevel":"substantial"},"disclosure":true,"devices":[{"role":"web"}]}`))
	})
	res, err := client.SessionStatus(context.Background(), testAPIKey, "s1", testSessionToken)
	if err != nil {
		t.Fatalf("SessionStatus: %v", err)
	}
	if res.Status != StatusApproved || res.AssuranceLevel != "high" || res.EIDASLevel != "substantial" ||
		res.CompletedAt == nil || res.Method != MethodYivi || res.Name != "" {
		t.Errorf("status = %+v", res)
	}
	if len(paths) != 1 || paths[0] != "/api/v1/sessions/s1/status" {
		t.Errorf("paths = %v, want only the status route", paths)
	}
}

// An IPS without the status route answers 404 there: the client reads the
// result instead and drops the name, so wallet and IPS deploy in any order.
func TestSessionStatusFallsBackToTheResultRoute(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/status") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"id":"s1","status":"approved","result":{"document":{"displayName":"Anna Jansen"}}}`))
	})
	res, err := client.SessionStatus(context.Background(), testAPIKey, "s1", testSessionToken)
	if err != nil || res.Status != StatusApproved || res.Name != "" {
		t.Errorf("SessionStatus = %+v, %v; want approved without the name", res, err)
	}
}

func TestCreateSessionSendsTheScriptedOutcome(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["scriptedOutcome"] != "reject:DOC_EXPIRED" {
			t.Errorf("scriptedOutcome = %v, want reject:DOC_EXPIRED", body["scriptedOutcome"])
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"s1","token":"tok","status":"rejected"}`))
	})
	if _, err := client.CreateSession(context.Background(), testAPIKey,
		SessionInput{ClientReference: "r1", ScriptedOutcome: "reject:DOC_EXPIRED"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
}

func TestStubResolvesAScriptedSessionAtOnce(t *testing.T) {
	stub := NewStub()
	sess, err := stub.CreateSession(context.Background(), "k", SessionInput{ScriptedOutcome: "reject:DOC_EXPIRED"})
	if err != nil || sess.Claim != nil {
		t.Fatalf("CreateSession = %+v, %v; want a scripted session without a claim", sess, err)
	}
	res, err := stub.SessionStatus(context.Background(), "k", sess.ID, sess.Token)
	if err != nil || res.Status != StatusRejected || res.ErrorCode != "DOC_EXPIRED" {
		t.Errorf("SessionStatus = %+v, %v; want rejected with DOC_EXPIRED", res, err)
	}
	if _, err := stub.CreateSession(context.Background(), "k", SessionInput{ScriptedOutcome: "bogus"}); err == nil {
		t.Error("an unknown script was accepted")
	}
}

func TestDecideReviewSendsTheDecision(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/api/v1/sessions/s1/decision" || body["status"] != "rejected" ||
			body["errorCode"] != "DOC_DAMAGED" || body["reason"] != "photo page torn" || body["reviewer"] != "sam@acme.test" {
			t.Errorf("decision %s = %v", r.URL.Path, body)
		}
		if r.Header.Get("Authorization") != "Bearer "+testSessionToken {
			t.Errorf("Authorization = %q, want the session token", r.Header.Get("Authorization"))
		}
	})
	err := client.DecideReview(context.Background(), testAPIKey, "s1", testSessionToken,
		ReviewDecision{ErrorCode: "DOC_DAMAGED", Reason: "photo page torn", Reviewer: "sam@acme.test"})
	if err != nil {
		t.Fatalf("DecideReview: %v", err)
	}
}

// A handover asks for the native slot and answers its link; an app still
// active comes back as CodeDeviceActive.
func TestSessionHandover(t *testing.T) {
	active := false
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/api/v1/sessions/s1/handover" || body["role"] != "native" ||
			r.Header.Get("Authorization") != "Bearer "+testSessionToken {
			t.Errorf("handover %s = %v", r.URL.Path, body)
		}
		if active {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"still active","code":"device_active"}`))
			return
		}
		_, _ = w.Write([]byte(`{"handoverToken":"h","role":"native","deepLink":"vcmrtd://verify?handover=h","expiresAt":"2026-09-29T10:00:00Z"}`))
	})
	claim, err := client.SessionHandover(context.Background(), testAPIKey, "s1", testSessionToken)
	if err != nil || claim.DeepLink != "vcmrtd://verify?handover=h" {
		t.Fatalf("SessionHandover = %+v, %v", claim, err)
	}
	active = true
	_, err = client.SessionHandover(context.Background(), testAPIKey, "s1", testSessionToken)
	var rejected *RejectedError
	if !errors.As(err, &rejected) || rejected.Code != CodeDeviceActive {
		t.Errorf("SessionHandover while active = %v, want %s", err, CodeDeviceActive)
	}
}

// SessionIdentity maps IPS's result onto the identity and its checks.
func TestSessionIdentityMapsTheResult(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sessions/s1/result" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"approved","completedAt":"2026-09-29T10:00:00Z","result":{
			"assurance":{"level":"substantial","eidasLevel":"substantial"},
			"document":{"type":"P","number":"NX1234567","issuingState":"NLD","nationality":"NLD",
				"firstName":"Anna","lastName":"Jansen","dateOfBirth":"1990-04-12","dateOfExpiry":"2031-02-01"},
			"chipChecks":{"passiveAuthentication":{"sodSignatureValid":true,"dataGroupHashesValid":true,"cscaTrustChainValid":false},
				"activeAuthentication":{"attempted":false}},
			"biometrics":{"faceMatchScore":0.81,"livenessResult":"passed"}},
			"devices":[{"role":"native"}]}`))
	})
	id, err := client.SessionIdentity(context.Background(), testAPIKey, "s1", testSessionToken)
	if err != nil {
		t.Fatalf("SessionIdentity: %v", err)
	}
	if id.Status != StatusApproved || id.Method != MethodIdem || id.GivenName != "Anna" || id.FamilyName != "Jansen" ||
		id.BirthDate != "1990-04-12" || id.EIDASLevel != "substantial" {
		t.Errorf("identity = %+v", id)
	}
	ev := id.Evidence
	if ev == nil || ev.Type != EvidenceEMRTD || ev.PassiveAuth != CheckInvalid || ev.ActiveAuth != CheckNotPerformed ||
		ev.FaceMatch == nil || *ev.FaceMatch != 0.81 || ev.Liveness != "passed" || ev.ExpiryDate != "2031-02-01" {
		t.Errorf("evidence = %+v; want the chip read with a failed trust chain and no active auth", ev)
	}
}

func TestListFlowsDecodesEachFlow(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/flows" || r.Header.Get(headerAPIKey) != testAPIKey {
			t.Errorf("list flows %s with key %q", r.URL.Path, r.Header.Get(headerAPIKey))
		}
		_, _ = w.Write([]byte(`[{"id":"f1","version":2,"active":true,"name":"Passport",
			"steps":["document_capture","nfc_read"],"requestedAttributes":["dg1"]}]`))
	})
	flows, err := client.ListFlows(context.Background(), testAPIKey)
	if err != nil || len(flows) != 1 {
		t.Fatalf("ListFlows = %+v, %v", flows, err)
	}
	if f := flows[0]; f.ID != "f1" || f.Version != 2 || !f.Active || f.Name != "Passport" || len(f.Steps) != 2 {
		t.Errorf("flow = %+v", f)
	}
}

func TestSubmitFaceFrameDecodesTheVerdict(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/app/tok/face" || r.Header.Get(headerAPIKey) != "" {
			t.Errorf("face frame %s with key %q; the token in the path is the credential", r.URL.Path, r.Header.Get(headerAPIKey))
		}
		_, _ = w.Write([]byte(`{"faceDetected":true,"matched":true,"consecutive":2,"stableFrames":3,
			"attempts":4,"maxAttempts":20,"decision":"pending"}`))
	})
	v, err := client.SubmitFaceFrame(context.Background(), "tok", "data:image/jpeg;base64,AA==")
	want := FaceVerdict{FaceDetected: true, Matched: true, Consecutive: 2, StableFrames: 3, Attempts: 4, MaxAttempts: 20, Decision: FaceDecisionPending}
	if err != nil || v != want {
		t.Errorf("SubmitFaceFrame = %+v, %v; want %+v", v, err, want)
	}
}
