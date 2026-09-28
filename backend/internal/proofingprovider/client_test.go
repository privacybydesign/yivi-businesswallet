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
			_, _ = w.Write([]byte(`{"id":"t1","name":"Acme","webhookSecret":"whsec_x"}`))
		case "/api/v1/admin/tenants/t1/keys":
			var body struct {
				Environment string   `json:"environment"`
				Scopes      []string `json:"scopes"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Environment != keyEnvironment || len(body.Scopes) != len(TenantKeyScopes) {
				t.Errorf("key request = %+v, want live with the tenant scopes", body)
			}
			_, _ = w.Write([]byte(`{"id":"k1","plaintext":"sk_live_abc"}`))
		}
	})

	tenant, err := client.CreateTenant(context.Background(), "Acme")
	if err != nil || tenant.ID != "t1" || tenant.WebhookSecret != "whsec_x" {
		t.Fatalf("CreateTenant = %+v, %v", tenant, err)
	}
	key, err := client.CreateAPIKey(context.Background(), tenant.ID, TenantKeyScopes)
	if err != nil || key != "sk_live_abc" {
		t.Fatalf("CreateAPIKey = %q, %v", key, err)
	}
	want := []string{"POST /api/v1/admin/tenants", "POST /api/v1/admin/tenants/t1/keys"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %v, want %v", paths, want)
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
