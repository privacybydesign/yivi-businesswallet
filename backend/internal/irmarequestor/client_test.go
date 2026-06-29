package irmarequestor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	irma "github.com/privacybydesign/irmago/irma"
	irmaserver "github.com/privacybydesign/irmago/irma/server"
)

const testAttr = "irma-demo.sidn-pbdf.email.email"

func newTestClient(t *testing.T, handler http.HandlerFunc, token string) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New(srv.URL, NewTokenAuthenticator(token), srv.Client())
}

func TestStartSessionPostsRequestAndReturnsPackage(t *testing.T) {
	const wantToken = "tok-123"

	var gotMethod, gotPath, gotAuth, gotContentType string
	var gotBody irma.DisclosureRequest

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(irmaserver.SessionPackage{
			Token: irma.RequestorToken(wantToken),
		})
	}, "preshared-secret")

	pkg, err := c.StartSession(context.Background(), irma.NewDisclosureRequest(irma.NewAttributeTypeIdentifier(testAttr)))
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/session" {
		t.Errorf("path = %q, want /session", gotPath)
	}
	if gotAuth != "preshared-secret" {
		t.Errorf("Authorization = %q, want the preshared token", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if string(pkg.Token) != wantToken {
		t.Errorf("returned token = %q, want %q", pkg.Token, wantToken)
	}
}

func TestStartSessionNoTokenSendsNoAuthHeader(t *testing.T) {
	authSeen := true // prove it gets set to false

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		authSeen = r.Header.Get("Authorization") != ""
		_ = json.NewEncoder(w).Encode(irmaserver.SessionPackage{Token: "t"})
	}, "")

	if _, err := c.StartSession(context.Background(), irma.NewDisclosureRequest(irma.NewAttributeTypeIdentifier(testAttr))); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if authSeen {
		t.Error("Authorization header was sent with an empty token; want none (no_auth dev posture)")
	}
}

func TestResultMapsSessionUnknownToUnknownSession(t *testing.T) {
	// The daemon returns an unknown/expired token as HTTP 400 with an
	// {"error":"SESSION_UNKNOWN"} envelope, NOT 404 (validated against the real
	// daemon). The sentinel mapping must key on the error code, not the status.
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":400,"error":"SESSION_UNKNOWN","description":"Unknown or expired session"}`))
	}, "")

	_, err := c.Result(context.Background(), "missing")
	if !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("Result error = %v, want ErrUnknownSession", err)
	}
}

func TestNonSessionUnknown400IsNotMappedToSentinel(t *testing.T) {
	// A different 400 (e.g. a malformed request) must NOT be swallowed as
	// "start over" — only SESSION_UNKNOWN is the unknown-session sentinel.
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":400,"error":"MALFORMED_INPUT","description":"bad request"}`))
	}, "")

	_, err := c.Result(context.Background(), "x")
	if errors.Is(err, ErrUnknownSession) {
		t.Fatal("a non-SESSION_UNKNOWN 400 was mis-mapped to ErrUnknownSession")
	}
	if err == nil {
		t.Fatal("expected an error for a 400 response")
	}
}

func TestStatusReturnsServerStatus(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session/tok/status" {
			t.Errorf("path = %q, want /session/tok/status", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(irmaserver.SessionResult{Status: irma.ServerStatusDone})
	}, "")

	got, err := c.Status(context.Background(), "tok")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got != irma.ServerStatusDone {
		t.Errorf("status = %q, want %q", got, irma.ServerStatusDone)
	}
}

func TestPingStartsThenCancels(t *testing.T) {
	const probeToken = "probe-tok"
	var started, cancelled bool

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			started = true
			_ = json.NewEncoder(w).Encode(irmaserver.SessionPackage{Token: irma.RequestorToken(probeToken)})
		case r.Method == http.MethodDelete && r.URL.Path == "/session/"+probeToken:
			cancelled = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}, "")

	if err := c.Ping(context.Background(), irma.NewAttributeTypeIdentifier(testAttr)); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if !started {
		t.Error("Ping did not start a session")
	}
	if !cancelled {
		t.Error("Ping did not cancel the probe session")
	}
}

func TestPingFailsWhenStartRejected(t *testing.T) {
	// A disclose_perms / scheme mismatch surfaces as a non-2xx on POST /session.
	// Ping must treat that as failure (the boot gate's whole purpose).
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			t.Error("Ping cancelled despite a failed start")
		}
		w.WriteHeader(http.StatusForbidden)
	}, "")

	if err := c.Ping(context.Background(), irma.NewAttributeTypeIdentifier(testAttr)); err == nil {
		t.Fatal("Ping succeeded despite the daemon rejecting the session start")
	}
}

func TestPingSucceedsWhenCancelFails(t *testing.T) {
	// Cancel is best-effort: a started-but-uncancelled probe is still a healthy
	// daemon. Ping must not fail just because cleanup did.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(irmaserver.SessionPackage{Token: "t"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError) // cancel fails
	}, "")

	if err := c.Ping(context.Background(), irma.NewAttributeTypeIdentifier(testAttr)); err != nil {
		t.Fatalf("Ping failed on a best-effort cancel error: %v", err)
	}
}
