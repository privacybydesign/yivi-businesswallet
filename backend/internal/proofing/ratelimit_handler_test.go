package proofing

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
