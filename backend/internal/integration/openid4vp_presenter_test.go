//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/devverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vppresenter"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// fakeInboundVerifier is the external verifier that invokes the business wallet:
// it hosts the Request Object at request_uri — signed with the identity the
// router trusts — and receives the direct_post Authorization Response at
// response_uri.
type fakeInboundVerifier struct {
	t        *testing.T
	identity devverifier.Identity
	server   *httptest.Server
	mu       sync.Mutex
	posted   []url.Values
}

const (
	inboundClientID = "x509_san_dns:verifier.test"
	inboundNonce    = "n-0S6_WzA2Mj"
	inboundState    = "state-123"
)

func newFakeInboundVerifier(t *testing.T, identity devverifier.Identity) *fakeInboundVerifier {
	t.Helper()
	f := &fakeInboundVerifier{t: t, identity: identity}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /request", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/oauth-authz-req+jwt")
		_, _ = w.Write(f.requestObject())
	})
	mux.HandleFunc("POST /response", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.posted = append(f.posted, r.PostForm)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"redirect_uri":"https://verifier.test/done"}`))
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

// requestObject is a JAR signed by the trusted relying-party identity, as the
// hosted Yivi verifier would sign one.
func (f *fakeInboundVerifier) requestObject() []byte {
	dcql, err := devverifier.SimpleDCQL("kvk", "nl.kvk.registration", nil)
	if err != nil {
		f.t.Fatal(err)
	}
	jar, err := devverifier.SignRequestObject(f.identity, devverifier.Request{
		Nonce:        inboundNonce,
		State:        inboundState,
		ResponseURI:  f.server.URL + "/response",
		ResponseMode: "direct_post",
		DCQLQuery:    dcql,
	})
	if err != nil {
		f.t.Fatalf("sign request object: %v", err)
	}
	return []byte(jar)
}

type startResp struct {
	ID string `json:"id"`
}

type statusResp struct {
	Status   string `json:"status"`
	Verifier string `json:"verifier"`
}

type orgRow struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type selectResp struct {
	Status      string `json:"status"`
	RedirectURI string `json:"redirectUri"`
}

func (e *testEnv) startInbound(t *testing.T, verifier *fakeInboundVerifier) string {
	t.Helper()
	resp := e.postJSON("/api/v1/openid4vp/start", map[string]any{
		"clientId":         inboundClientID,
		"requestUri":       verifier.server.URL + "/request",
		"requestUriMethod": "get",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("start = %d, want 201", resp.StatusCode)
	}
	return decodeJSON[startResp](t, resp).ID
}

// TestOpenID4VPInboundFlow drives the whole invocation seam through the assembled
// router: start (pre-auth) → status names the verifier → orgs after login lists
// only the caller's memberships → select behind the tenant seam delivers the
// stub holder's vp_token by direct_post with the request's state → the row is
// consumed (a second select is a conflict) and the org's audit log has the trail.
func TestOpenID4VPInboundFlow(t *testing.T) {
	env := setup(t)
	verifier := newFakeInboundVerifier(t, env.verifier)

	// A fresh browser: no session yet.
	id := env.startInbound(t, verifier)

	resp := env.do(http.MethodGet, "/api/v1/openid4vp/"+id+"/status", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if st := decodeJSON[statusResp](t, resp); st.Status != openid4vppresenter.StatusPendingAuth || st.Verifier != "verifier.test" {
		t.Fatalf("status = %+v", st)
	}

	// The org picker is behind authentication.
	resp = env.do(http.MethodGet, "/api/v1/openid4vp/"+id+"/orgs", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("orgs unauthenticated = %d, want 401", resp.StatusCode)
	}

	me := env.login("member@acme.test")
	acme := env.createOrg("Acme", "acme")
	env.addMembership(me.ID, acme, organization.RoleMember)
	env.createOrg("Other", "other") // not a member: must not be offered

	resp = env.do(http.MethodGet, "/api/v1/openid4vp/"+id+"/orgs", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("orgs = %d, want 200", resp.StatusCode)
	}
	orgs := decodeJSON[[]orgRow](t, resp)
	if len(orgs) != 1 || orgs[0].Slug != "acme" || orgs[0].Name != "Acme" {
		t.Fatalf("orgs = %+v, want only acme", orgs)
	}

	// Selecting an org the caller is not a member of is refused by the tenant seam
	// before the slice sees it.
	resp = env.do(http.MethodPost, "/api/v1/orgs/other/openid4vp/"+id+"/select", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("select non-member org = %d, want 403", resp.StatusCode)
	}

	resp = env.do(http.MethodPost, "/api/v1/orgs/acme/openid4vp/"+id+"/select", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("select = %d, want 200", resp.StatusCode)
	}
	sel := decodeJSON[selectResp](t, resp)
	if sel.Status != openid4vppresenter.StatusCompleted || sel.RedirectURI != "https://verifier.test/done" {
		t.Fatalf("select = %+v", sel)
	}

	verifier.mu.Lock()
	posted := verifier.posted
	verifier.mu.Unlock()
	if len(posted) != 1 {
		t.Fatalf("verifier received %d responses, want 1", len(posted))
	}
	if got := posted[0].Get("state"); got != inboundState {
		t.Errorf("state = %q, want %q", got, inboundState)
	}
	var vpToken map[string][]string
	if err := json.Unmarshal([]byte(posted[0].Get("vp_token")), &vpToken); err != nil {
		t.Fatalf("vp_token is not JSON: %v", err)
	}
	if len(vpToken["kvk"]) != 1 {
		t.Errorf("vp_token = %v, want one entry for credential query kvk", vpToken)
	}

	// One-time use.
	resp = env.do(http.MethodPost, "/api/v1/orgs/acme/openid4vp/"+id+"/select", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second select = %d, want 409", resp.StatusCode)
	}
	resp = env.do(http.MethodGet, "/api/v1/openid4vp/"+id+"/status", nil)
	if st := decodeJSON[statusResp](t, resp); st.Status != openid4vppresenter.StatusCompleted {
		t.Fatalf("status after completion = %+v", st)
	}

	// The org's audit trail: selection and completion, never the query or the
	// response URI.
	rows, err := env.pool.Query(context.Background(),
		`SELECT action, metadata::text FROM audit_events WHERE organization_id = $1 ORDER BY occurred_at`, acme)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var action, meta string
		if err := rows.Scan(&action, &meta); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
		for _, forbidden := range []string{"dcql", "response_uri", "/response", inboundNonce} {
			if strings.Contains(meta, forbidden) {
				t.Errorf("audit metadata for %s leaks %q: %s", action, forbidden, meta)
			}
		}
	}
	if len(actions) != 2 || actions[0] != "presentation.org_selected" || actions[1] != "presentation.completed" {
		t.Fatalf("org audit actions = %v", actions)
	}
	var requested int
	if err := env.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_events WHERE action = 'presentation.requested' AND organization_id IS NULL`).Scan(&requested); err != nil {
		t.Fatal(err)
	}
	if requested != 1 {
		t.Errorf("presentation.requested events = %d, want 1 (pre-auth, no org)", requested)
	}
}

// A transaction is bound to the first authenticated user who resumes it; a second
// user with the same opaque id is refused, and cannot pick their own org for it.
func TestOpenID4VPInboundBoundToFirstUser(t *testing.T) {
	env := setup(t)
	verifier := newFakeInboundVerifier(t, env.verifier)
	id := env.startInbound(t, verifier)

	first := env.loginAs("first@example.test")
	env.addMembership(first, env.createOrg("First", "first"), organization.RoleMember)
	resp := env.do(http.MethodGet, "/api/v1/openid4vp/"+id+"/orgs", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first orgs = %d, want 200", resp.StatusCode)
	}

	second := env.loginAs("second@example.test")
	env.addMembership(second, env.createOrg("Second", "second"), organization.RoleMember)
	resp = env.do(http.MethodGet, "/api/v1/openid4vp/"+id+"/orgs", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("second user's orgs = %d, want 403", resp.StatusCode)
	}
	resp = env.do(http.MethodPost, "/api/v1/orgs/second/openid4vp/"+id+"/select", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("second user's select = %d, want 403", resp.StatusCode)
	}
}

// The invocation forms the design rejects, and the metadata document on the root
// mux (not under /api/v1, not the SPA).
func TestOpenID4VPInboundRejectionsAndMetadata(t *testing.T) {
	env := setup(t)
	verifier := newFakeInboundVerifier(t, env.verifier)

	cases := []struct {
		name string
		body map[string]any
		code string
	}{
		{"by value", map[string]any{"clientId": inboundClientID, "request": "a.b.c"}, "invalid_request"},
		{"both", map[string]any{"clientId": inboundClientID, "request": "a.b.c", "requestUri": verifier.server.URL + "/request"}, "invalid_request"},
		{"neither", map[string]any{"clientId": inboundClientID}, "invalid_request"},
		{"bad method", map[string]any{"clientId": inboundClientID, "requestUri": verifier.server.URL + "/request", "requestUriMethod": "put"}, "invalid_request_uri_method"},
		{"client_id mismatch", map[string]any{"clientId": "x509_san_dns:impostor.test", "requestUri": verifier.server.URL + "/request"}, "invalid_request_object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := env.postJSON("/api/v1/openid4vp/start", tc.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			body := decodeJSON[map[string]string](t, resp)
			if body["code"] != tc.code {
				t.Fatalf("code = %q, want %q", body["code"], tc.code)
			}
		})
	}

	// Nothing was persisted for the rejected invocations.
	var n int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM openid4vp_transactions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("openid4vp_transactions rows = %d, want 0", n)
	}

	resp := env.do(http.MethodGet, openid4vppresenter.WellKnownPath, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("well-known = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("well-known Content-Type = %q", ct)
	}
	doc := decodeJSON[map[string]any](t, resp)
	if doc["authorization_endpoint"] != "http://app.test/openid4vp" {
		t.Fatalf("authorization_endpoint = %v", doc["authorization_endpoint"])
	}
}
