package useravatar

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Register's patterns are string literals, so a typo would only show up as a 404
// at runtime. This resolves each one against a real ServeMux (and would panic on
// a pattern that conflicts with another in this set).
func TestRegisterRoutesResolve(t *testing.T) {
	passthrough := func(next http.Handler) http.Handler { return next }
	h := NewHandler(&fakeStore{}, passthrough, passthrough)
	mux := http.NewServeMux()
	h.Register(mux)

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/me/avatar"},
		{http.MethodPut, "/me/avatar"},
		{http.MethodDelete, "/me/avatar"},
		{http.MethodGet, "/orgs/acme/members/8f14e45f-ceea-467a-9575-1b1b1b1b1b1b/avatar"},
	}
	for _, r := range routes {
		_, pattern := mux.Handler(httptest.NewRequest(r.method, r.path, nil))
		if pattern == "" {
			t.Errorf("%s %s resolves to no registered pattern", r.method, r.path)
		}
	}
}
