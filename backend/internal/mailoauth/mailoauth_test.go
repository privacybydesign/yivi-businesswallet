package mailoauth

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

// Microsoft's error document names what is wrong (a wrong or expired secret, a
// permission nobody consented to), and that is what an admin has to read. What
// carries it are the two closed shapes: the RFC 6749 error code, and the AADSTS
// code an admin searches for. The free text after that code is the responder's
// own bytes and goes with the rest of the description.
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
	for _, want := range []string{"401 Unauthorized", "invalid_client", "AADSTS7000215"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to carry %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "Invalid client secret provided") {
		t.Errorf("err = %v, want the free-text description dropped", err)
	}
}

// rawResponder answers with a byte-for-byte HTTP response. It takes a raw listener
// rather than httptest to reach one position an httptest stub structurally cannot:
// ResponseWriter.WriteHeader makes Go write its own canonical reason phrase, so a
// stub can never put chosen bytes in the status line.
func rawResponder(t *testing.T, response string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// Read the request in full, body included, so the client's write completes
			// before the connection goes away under it.
			if req, err := http.ReadRequest(bufio.NewReader(conn)); err == nil {
				_, _ = io.Copy(io.Discard, req.Body)
			}
			_, _ = io.WriteString(conn, response)
			_ = conn.Close()
		}
	}()
	return "http://" + listener.Addr().String()
}

// refusal renders a 401 with a chosen reason phrase and body.
func refusal(reasonPhrase, body string) string {
	return fmt.Sprintf("HTTP/1.1 401 %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		reasonPhrase, len(body), body)
}

// The client secret goes out in the request's form body, so a response that quotes
// the request back carries it into the error — and that error is logged at ERROR by
// respond/handler.go for the settings self-test and notifications/dispatcher.go for
// a background notification. The responder is not necessarily the tenant:
// WithEndpoint is a supported mode for a sovereign cloud, and a TLS-terminating
// egress proxy or captive portal in front of login.microsoftonline.com is an
// ordinary deployment condition. These are the positions such a responder controls.
func TestTokenNeverRepeatsTheRequestFromAResponse(t *testing.T) {
	const quoted = "client_id=c&client_secret=SUPERSECRET&grant_type=client_credentials"
	creds := testCredentials()
	creds.ClientSecret = "SUPERSECRET"

	for name, response := range map[string]string{
		"status line reason phrase":   refusal("Unauthorized for "+quoted, `{"error":"invalid_client"}`),
		"the error code itself":       refusal("Unauthorized", `{"error":"`+quoted+`"}`),
		"a free-text description":     refusal("Unauthorized", `{"error":"invalid_client","error_description":"`+quoted+`"}`),
		"an intermediary's HTML page": refusal("Unauthorized", "<html><body>"+quoted+"</body></html>"),
		"a description after a code":  refusal("Unauthorized", `{"error":"invalid_client","error_description":"AADSTS7000215: `+quoted+`"}`),
	} {
		t.Run(name, func(t *testing.T) {
			source := NewMicrosoft(http.DefaultClient).WithEndpoint(rawResponder(t, response))

			_, err := source.Token(context.Background(), creds)
			if err == nil {
				t.Fatal("a refused credential produced a token")
			}
			if strings.Contains(err.Error(), "SUPERSECRET") {
				t.Errorf("err = %v, want no part of the client secret in it", err)
			}
			// The status this side read stays, so the reason is not empty.
			if !strings.Contains(err.Error(), "401 Unauthorized") {
				t.Errorf("err = %v, want this side's own status line", err)
			}
		})
	}
}

// An error code outside RFC 6749's set is not one of the endpoint's refusals but
// bytes the responder chose, so it is dropped rather than repeated. It costs the
// reason's detail and leaks nothing, the same trade slackchannel's allowlist makes.
func TestTokenDropsAnUnlistedErrorCode(t *testing.T) {
	source := NewMicrosoft(http.DefaultClient).WithEndpoint(
		rawResponder(t, refusal("Unauthorized", `{"error":"temporarily_unavailable"}`)))

	_, err := source.Token(context.Background(), testCredentials())
	if err == nil {
		t.Fatal("a refused credential produced a token")
	}
	if strings.Contains(err.Error(), "temporarily_unavailable") {
		t.Errorf("err = %v, want an unlisted code dropped", err)
	}
}

// Concurrent sends for one org are the realistic cache miss, and the case the
// cache's own justification is weakest in: notifications.Dispatcher runs each
// Channel.Notify on its own goroutine, so several outbox rows for one org fan out
// at once and each would otherwise spend a request against an endpoint that is
// rate-limited per tenant.
func TestTokenSharesOneExchangeAcrossConcurrentCallers(t *testing.T) {
	const callers = 20
	release := make(chan struct{})
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		<-release
		_, _ = fmt.Fprint(w, `{"token_type":"Bearer","expires_in":3600,"access_token":"shared-token"}`)
	}))
	defer server.Close()
	source := NewMicrosoft(server.Client()).WithEndpoint(server.URL)

	tokens := make([]string, callers)
	errs := make([]error, callers)
	var started, finished sync.WaitGroup
	started.Add(callers)
	finished.Add(callers)
	for i := range callers {
		go func() {
			defer finished.Done()
			started.Done()
			tokens[i], errs[i] = source.Token(context.Background(), testCredentials())
		}()
	}
	// Every caller has entered Token, so the exchange they share is in flight.
	// A straggler that arrives after it finishes reads the cache, which is the
	// same one request.
	started.Wait()
	close(release)
	finished.Wait()

	if got := calls.Load(); got != 1 {
		t.Errorf("token endpoint called %d times for %d concurrent callers, want 1", got, callers)
	}
	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if tokens[i] != "shared-token" {
			t.Errorf("caller %d token = %q, want the shared token", i, tokens[i])
		}
	}
	if left := inFlightSize(source); left != 0 {
		t.Errorf("%d exchanges left in flight, want 0; a stranded one wedges every later caller", left)
	}
}

// A failed exchange has to leave the in-flight map clean too: an entry nobody ever
// closes would leave every later caller for that org waiting on it until its own
// deadline passed.
func TestTokenClearsTheInFlightEntryAfterAFailure(t *testing.T) {
	source := NewMicrosoft(http.DefaultClient).WithEndpoint(
		rawResponder(t, refusal("Unauthorized", `{"error":"invalid_client"}`)))

	if _, err := source.Token(context.Background(), testCredentials()); err == nil {
		t.Fatal("a refused credential produced a token")
	}
	if left := inFlightSize(source); left != 0 {
		t.Errorf("%d exchanges left in flight after a failure, want 0", left)
	}
}

// Rotating a secret changes the cache key, so the entry the old secret minted is
// stranded — nothing looks it up again. Without eviction a long-lived process keeps
// one per rotation per org for as long as it runs.
func TestTokenEvictsLapsedCacheEntries(t *testing.T) {
	const rotations = 5
	ts := newTokenServer(t, 3600)
	source := newTestSource(t, ts)
	now := time.Now()
	source.now = func() time.Time { return now }
	ctx := context.Background()

	for i := range rotations {
		creds := testCredentials()
		creds.ClientSecret = fmt.Sprintf("rotation-%d", i)
		if _, err := source.Token(ctx, creds); err != nil {
			t.Fatalf("Token (rotation %d): %v", i, err)
		}
		// Past the cached lifetime, so the entry this rotation leaves behind is dead
		// by the time the next one is stored.
		now = now.Add(time.Hour)
	}

	if got := cacheSize(source); got != 1 {
		t.Errorf("cache holds %d entries after %d rotations, want 1", got, rotations)
	}
}

// The entry for a credential still in use is overwritten in place, so refreshing a
// token does not accumulate one entry per refresh either.
func TestTokenRefreshOverwritesTheEntryInPlace(t *testing.T) {
	ts := newTokenServer(t, 3600)
	source := newTestSource(t, ts)
	now := time.Now()
	source.now = func() time.Time { return now }
	ctx := context.Background()

	for range 5 {
		if _, err := source.Token(ctx, testCredentials()); err != nil {
			t.Fatalf("Token: %v", err)
		}
		now = now.Add(time.Hour)
	}

	if got := cacheSize(source); got != 1 {
		t.Errorf("cache holds %d entries after 5 refreshes, want 1", got)
	}
}

func cacheSize(m *Microsoft) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.cache)
}

func inFlightSize(m *Microsoft) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.inFlight)
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
