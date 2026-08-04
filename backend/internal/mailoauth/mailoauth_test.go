package mailoauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func testCredentials() Credentials {
	return Credentials{TenantID: "tenant-1", ClientID: "client-1", ClientSecret: "s3cret"}
}

// tokenServer answers the client-credentials exchange the way the Microsoft
// identity platform does, and records what it was asked.
type tokenServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []map[string]string
	paths    []string
}

func newTokenServer(t *testing.T, expiresIn int) *tokenServer {
	t.Helper()
	ts := &tokenServer{}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		form := map[string]string{}
		for key := range r.PostForm {
			form[key] = r.PostForm.Get(key)
		}
		ts.mu.Lock()
		ts.requests = append(ts.requests, form)
		ts.paths = append(ts.paths, r.URL.Path)
		count := len(ts.requests)
		ts.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"token_type":"Bearer","expires_in":%d,"access_token":"access-token-%d"}`, expiresIn, count)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func (ts *tokenServer) calls() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return len(ts.requests)
}

func newTestSource(t *testing.T, ts *tokenServer) *Microsoft {
	t.Helper()
	return NewMicrosoft(ts.Client()).WithEndpoint(ts.URL)
}

// The exchange is app-only client credentials against the tenant's token
// endpoint, scoped to Exchange Online's own SMTP permission.
func TestTokenExchangesClientCredentials(t *testing.T) {
	ts := newTokenServer(t, 3600)
	source := newTestSource(t, ts)

	token, err := source.Token(context.Background(), testCredentials())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if token != "access-token-1" {
		t.Errorf("token = %q, want the endpoint's access token", token)
	}
	if got, want := ts.paths[0], "/tenant-1/oauth2/v2.0/token"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	form := ts.requests[0]
	for key, want := range map[string]string{
		"client_id":     "client-1",
		"client_secret": "s3cret",
		"grant_type":    "client_credentials",
		"scope":         outlookScope,
	} {
		if form[key] != want {
			t.Errorf("%s = %q, want %q", key, form[key], want)
		}
	}
}

// A notification to an org's admins sends one message per recipient, and a bulk
// credential offer one per person. Minting a token for each would hit the token
// endpoint's per-tenant rate limit for no gain, since the token outlives the run.
func TestTokenReusesACachedToken(t *testing.T) {
	ts := newTokenServer(t, 3600)
	source := newTestSource(t, ts)
	ctx := context.Background()

	first, err := source.Token(ctx, testCredentials())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	second, err := source.Token(ctx, testCredentials())
	if err != nil {
		t.Fatalf("Token (second): %v", err)
	}
	if first != second {
		t.Errorf("second token = %q, want the cached %q", second, first)
	}
	if ts.calls() != 1 {
		t.Errorf("token endpoint called %d times, want 1", ts.calls())
	}
}

// A rotated secret is a different credential and must not be served the token the
// old one minted — an org that rotates because the old secret leaked would keep
// sending with it until the cache lapsed.
func TestTokenMissesTheCacheOnARotatedSecret(t *testing.T) {
	ts := newTokenServer(t, 3600)
	source := newTestSource(t, ts)
	ctx := context.Background()

	if _, err := source.Token(ctx, testCredentials()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	rotated := testCredentials()
	rotated.ClientSecret = "rotated"
	if _, err := source.Token(ctx, rotated); err != nil {
		t.Fatalf("Token (rotated): %v", err)
	}
	if ts.calls() != 2 {
		t.Errorf("token endpoint called %d times, want 2", ts.calls())
	}
	if ts.requests[1]["client_secret"] != "rotated" {
		t.Errorf("client_secret = %q, want the rotated secret", ts.requests[1]["client_secret"])
	}
}

// A cached token is dropped before it lapses, so a send never authenticates with
// one that expires mid-exchange.
func TestTokenRefreshesAnExpiredToken(t *testing.T) {
	ts := newTokenServer(t, 3600)
	source := newTestSource(t, ts)
	now := time.Now()
	source.now = func() time.Time { return now }
	ctx := context.Background()

	if _, err := source.Token(ctx, testCredentials()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	// Past the cached lifetime (an hour less the safety margin).
	now = now.Add(time.Hour)
	second, err := source.Token(ctx, testCredentials())
	if err != nil {
		t.Fatalf("Token (after expiry): %v", err)
	}
	if second != "access-token-2" {
		t.Errorf("token = %q, want a freshly minted one", second)
	}
	if ts.calls() != 2 {
		t.Errorf("token endpoint called %d times, want 2", ts.calls())
	}
}

// A token that lapses within the safety margin is used but not cached, so the
// next send does not pick up something already too old to present.
func TestTokenDoesNotCacheAShortLivedToken(t *testing.T) {
	ts := newTokenServer(t, 30)
	source := newTestSource(t, ts)
	ctx := context.Background()

	if _, err := source.Token(ctx, testCredentials()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if _, err := source.Token(ctx, testCredentials()); err != nil {
		t.Fatalf("Token (second): %v", err)
	}
	if ts.calls() != 2 {
		t.Errorf("token endpoint called %d times, want 2", ts.calls())
	}
}

// An unfinished configuration and a refused credential send an admin to
// different places, so they are different errors.
func TestTokenReportsIncompleteCredentials(t *testing.T) {
	source := NewMicrosoft(http.DefaultClient)
	full := testCredentials()

	for name, creds := range map[string]Credentials{
		"no tenant": {ClientID: full.ClientID, ClientSecret: full.ClientSecret},
		"no client": {TenantID: full.TenantID, ClientSecret: full.ClientSecret},
		"no secret": {TenantID: full.TenantID, ClientID: full.ClientID},
	} {
		if _, err := source.Token(context.Background(), creds); !errors.Is(err, ErrIncompleteConfig) {
			t.Errorf("%s: err = %v, want ErrIncompleteConfig", name, err)
		}
	}
}

// Microsoft's error document names what is wrong (an expired secret, a
// permission nobody consented to), and that is what an admin has to read.
func TestTokenCarriesTheEndpointsRefusal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"error":"invalid_client","error_description":"AADSTS7000215: Invalid client secret provided."}`)
	}))
	defer server.Close()
	source := NewMicrosoft(server.Client()).WithEndpoint(server.URL)

	_, err := source.Token(context.Background(), testCredentials())
	if err == nil {
		t.Fatal("a refused credential produced a token")
	}
	if !strings.Contains(err.Error(), "AADSTS7000215") {
		t.Errorf("err = %v, want it to carry the endpoint's reason", err)
	}
}

// A 200 with no token is not a token: obeying it would authenticate with an
// empty bearer and read as a rejected mailbox.
func TestTokenRejectsAResponseWithoutAToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"token_type":"Bearer","expires_in":3600}`)
	}))
	defer server.Close()
	source := NewMicrosoft(server.Client()).WithEndpoint(server.URL)

	if _, err := source.Token(context.Background(), testCredentials()); err == nil {
		t.Fatal("a response with no access token was accepted")
	}
}

// The caller's deadline bounds the token request: it is on the path of a send,
// and a hung identity platform must not hold the sending goroutine.
func TestTokenHonoursTheContext(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	source := NewMicrosoft(server.Client()).WithEndpoint(server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.Token(ctx, testCredentials()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
