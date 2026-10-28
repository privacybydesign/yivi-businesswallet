package proofing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/clientip"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// Unknown API keys and hosted links from one source answer 429 once past
// authFailureLimit, instead of 401 or 404 for as long as it guesses.
func TestUnknownCredentialsLimited(t *testing.T) {
	f, _ := newDataFixture(FlowIdentity)
	pass := func(next http.Handler) http.Handler { return next }
	h := NewHandler(f.svc, pass, pass)
	mux := http.NewServeMux()
	mux.Handle("GET /proofing/flows", h.requireAPIKey(ScopeFlowsRead, func(http.ResponseWriter, *http.Request) error { return nil }))
	mux.Handle("GET /proof/{token}", respond.HandlerFunc(h.limitHosted(func(http.ResponseWriter, *http.Request) error { return nil })))

	for name, request := range map[string]func() *http.Request{
		"api key": func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/proofing/flows", nil)
			r.Header.Set(headerAuthorization, bearerPrefix+apiKeyPrefix+"guess")
			return r
		},
		"hosted link": func() *http.Request { return httptest.NewRequest(http.MethodGet, "/proof/guess", nil) },
	} {
		t.Run(name, func(t *testing.T) {
			h.authFailures = newAuthFailures()
			var last int
			for range authFailureLimit.Burst + 1 {
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, request())
				last = rec.Code
			}
			if last != http.StatusTooManyRequests {
				t.Errorf("guess %d = %d, want 429", authFailureLimit.Burst+1, last)
			}
		})
	}
}

// The failure limiter is keyed on the source, which a NAT shares among its
// callers: once it is spent by guesses, a valid hosted link,
// its page's frame origins and a valid API key are still served; only
// another guess is refused. The hosted page's guesses count against it too.
func TestValidCredentialsPassSpentFailures(t *testing.T) {
	f := newFixture()
	_, token := f.sendHosted(t)
	if _, err := saveOrigins(f.svc, []string{"https://portal.acme.example"}); err != nil {
		t.Fatalf("saveOrigins: %v", err)
	}
	const secret = apiKeyPrefix + "valid"
	f.svc.apiKeys = oneValidKey{secret: secret, caller: APIKeyCaller{
		KeyID: uuid.New(), Org: testOrg, Scopes: APIKeyScopes,
	}}
	pass := func(next http.Handler) http.Handler { return next }
	h := NewHandler(f.svc, pass, pass)
	ok := func(http.ResponseWriter, *http.Request) error { return nil }
	mux := http.NewServeMux()
	mux.Handle("GET /proofing/flows", h.requireAPIKey(ScopeFlowsRead, ok))
	mux.Handle("GET /proof/{token}", respond.HandlerFunc(h.limitHosted(ok)))
	serve := func(r *http.Request) int {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec.Code
	}
	apiCall := func(key string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/proofing/flows", nil)
		r.Header.Set(headerAuthorization, bearerPrefix+key)
		return r
	}
	csp := func(path string) string {
		header := http.Header{}
		h.PageHeaders(httptest.NewRequest(http.MethodGet, path, nil), header)
		return header.Get(headerCSP)
	}

	for range authFailureLimit.Burst {
		csp("/p/guess")
	}
	if code := serve(httptest.NewRequest(http.MethodGet, "/proof/guess", nil)); code != http.StatusTooManyRequests {
		t.Errorf("hosted guess after %d page guesses = %d, want 429", authFailureLimit.Burst, code)
	}
	if code := serve(apiCall(apiKeyPrefix + "guess")); code != http.StatusTooManyRequests {
		t.Errorf("API key guess with the failures spent = %d, want 429", code)
	}
	if code := serve(httptest.NewRequest(http.MethodGet, "/proof/"+token, nil)); code != http.StatusOK {
		t.Errorf("valid hosted link with the failures spent = %d, want 200", code)
	}
	if got := csp("/p/" + token); got == frameNone {
		t.Errorf("valid link's page with the failures spent: CSP = %q, want its origins", got)
	}
	if code := serve(apiCall(secret)); code != http.StatusOK {
		t.Errorf("valid API key with the failures spent = %d, want 200", code)
	}
}

// One source's hosted calls, a valid link's included, answer 429 once past
// hostedSourceLimit, and its page may be framed by none; another source with
// the same link is still served. The source is the client address the
// trusted proxy reports, not the proxy.
func TestHostedSourceLimited(t *testing.T) {
	f := newFixture()
	_, token := f.sendHosted(t)
	if _, err := saveOrigins(f.svc, []string{"https://portal.acme.example"}); err != nil {
		t.Fatalf("saveOrigins: %v", err)
	}
	pass := func(next http.Handler) http.Handler { return next }
	h := NewHandler(f.svc, pass, pass)
	mux := http.NewServeMux()
	mux.Handle("GET /proof/{token}", respond.HandlerFunc(h.limitHosted(func(http.ResponseWriter, *http.Request) error { return nil })))
	trusted, err := clientip.ParseTrusted([]string{ingressPeer})
	if err != nil {
		t.Fatalf("ParseTrusted: %v", err)
	}
	withClientIP := clientip.Middleware(clientip.NewResolver(trusted))
	var captured *http.Request
	capture := withClientIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { captured = r }))
	from := func(client, path string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.RemoteAddr = ingressPeer + ":443"
		r.Header.Set("X-Forwarded-For", "198.51.100.1, "+client)
		capture.ServeHTTP(httptest.NewRecorder(), r)
		return captured
	}
	serve := func(r *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec
	}
	csp := func(r *http.Request) string {
		header := http.Header{}
		h.PageHeaders(r, header)
		return header.Get(headerCSP)
	}

	const busy, other = "203.0.113.7", "203.0.113.8"
	for i := range hostedSourceLimit.Burst {
		if code := serve(from(busy, "/proof/"+token)).Code; code != http.StatusOK {
			t.Fatalf("call %d within the limit = %d, want 200", i+1, code)
		}
	}
	rec := serve(from(busy, "/proof/"+token))
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get(headerRetryAfter) == "" {
		t.Errorf("valid link past the source's limit = %d (Retry-After %q), want 429 with Retry-After",
			rec.Code, rec.Header().Get(headerRetryAfter))
	}
	if got := csp(from(busy, "/p/"+token)); got != frameNone {
		t.Errorf("page past the source's limit: CSP = %q, want %q", got, frameNone)
	}
	if code := serve(from(other, "/proof/"+token)).Code; code != http.StatusOK {
		t.Errorf("same link from another source = %d, want 200", code)
	}
	if got := csp(from(other, "/p/"+token)); got == frameNone {
		t.Errorf("page from another source: CSP = %q, want its origins", got)
	}
}

// ingressPeer is the trusted proxy the hosted source tests arrive through.
const ingressPeer = "10.0.0.1"

// A context that already carries a link's read is not read again for its
// limit key, nor by the handler's own read after.
func TestHostedLimitKeyReadsOnce(t *testing.T) {
	f := newFixture()
	_, token := f.sendHosted(t)
	ctx, _, err := f.svc.hostedLimitKey(context.Background(), token)
	if err != nil {
		t.Fatalf("hostedLimitKey: %v", err)
	}
	if ctx, _, err = f.svc.hostedLimitKey(ctx, token); err != nil {
		t.Fatalf("second hostedLimitKey: %v", err)
	}
	if _, err := f.svc.HostedRequest(ctx, token); err != nil {
		t.Fatalf("HostedRequest: %v", err)
	}
	if f.requests.linkLookups != 1 {
		t.Errorf("link read %d times, want once", f.requests.linkLookups)
	}
}

// oneValidKey authenticates its one secret as caller, and no other key.
type oneValidKey struct {
	fakeAPIKeys
	secret string
	caller APIKeyCaller
}

func (k oneValidKey) Authenticate(_ context.Context, raw string) (APIKeyCaller, error) {
	if raw != k.secret {
		return APIKeyCaller{}, ErrAPIKeyInvalid
	}
	return k.caller, nil
}
