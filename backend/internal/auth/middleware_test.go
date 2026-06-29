package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/session"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

type fakeLookuper struct {
	user user.User
	err  error
	got  string
}

func (f *fakeLookuper) Lookup(_ context.Context, rawToken string) (user.User, error) {
	f.got = rawToken
	return f.user, f.err
}

func TestRequireUser_NoCookie(t *testing.T) {
	lk := &fakeLookuper{err: session.ErrInvalidSession}
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	rec := httptest.NewRecorder()
	RequireUser(lk)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if called {
		t.Fatal("next must not be called without a cookie")
	}
	if lk.got != "" {
		t.Fatal("Lookup must not be called when no cookie is present")
	}
	assertUnauthBody(t, rec)
}

func TestRequireUser_InvalidSession(t *testing.T) {
	lk := &fakeLookuper{err: session.ErrInvalidSession}
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "bad-or-expired"})
	rec := httptest.NewRecorder()
	RequireUser(lk)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if called {
		t.Fatal("next must not be called for an invalid session")
	}
	if lk.got != "bad-or-expired" {
		t.Fatalf("Lookup got %q, want the raw cookie token", lk.got)
	}
	assertUnauthBody(t, rec)
}

func TestRequireUser_ValidSession_InjectsUser(t *testing.T) {
	want := user.User{ID: uuid.New(), Email: "user@example.test"}
	lk := &fakeLookuper{user: want}

	var seen user.User
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = UserFromContext(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "good"})
	rec := httptest.NewRecorder()
	RequireUser(lk)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if seen != want {
		t.Fatalf("context user = %+v, want %+v", seen, want)
	}
}

func assertUnauthBody(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if body["code"] != unauthenticatedCode {
		t.Fatalf("body code = %v, want %q", body["code"], unauthenticatedCode)
	}
}
