package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// stubPinger satisfies the Pinger interface for router construction in tests.
type stubPinger struct{}

func (stubPinger) Ping(context.Context) error { return nil }

func writeStaticSite(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>index"), 0o600); err != nil {
		t.Fatal(err)
	}
	assets := filepath.Join(dir, "assets")
	if err := os.Mkdir(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "app.js"), []byte("console.log(1)"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSPA_ServesIndexAtRoot(t *testing.T) {
	h := New(stubPinger{}, writeStaticSite(t))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := rec.Body.String(); body != "<!doctype html>index" {
		t.Fatalf("expected index.html body, got %q", body)
	}
}

func TestSPA_ServesExistingAsset(t *testing.T) {
	h := New(stubPinger{}, writeStaticSite(t))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := rec.Body.String(); body != "console.log(1)" {
		t.Fatalf("expected asset body, got %q", body)
	}
}

func TestSPA_FallsBackToIndexForClientRoute(t *testing.T) {
	h := New(stubPinger{}, writeStaticSite(t))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/orgs/acme/settings", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (SPA fallback), got %d", rec.Code)
	}
	if body := rec.Body.String(); body != "<!doctype html>index" {
		t.Fatalf("expected index.html fallback, got %q", body)
	}
}

// The SPA handler must never shadow the health endpoints or the API subtree:
// ServeMux always prefers the more specific pattern.
func TestSPA_DoesNotShadowHealthEndpoints(t *testing.T) {
	h := New(stubPinger{}, writeStaticSite(t))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, livePath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from %s, got %d", livePath, rec.Code)
	}
	if body := rec.Body.String(); body != "" {
		t.Fatalf("expected empty liveness body, got SPA content %q", body)
	}
}

// Traversal outside the static root must not escape it; filepath.Join cleans the
// path, so "../" segments collapse and the request resolves inside staticDir
// (here: the SPA index fallback), never a parent-directory file.
func TestSPA_RejectsTraversal(t *testing.T) {
	dir := writeStaticSite(t)
	secret := filepath.Join(filepath.Dir(dir), "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	h := New(stubPinger{}, dir)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/../secret.txt", nil))

	if body := rec.Body.String(); body == "top secret" {
		t.Fatal("traversal escaped static root and leaked a parent-directory file")
	}
}

// With no static dir configured (dev), unmatched paths must 404 rather than be
// served as an SPA.
func TestSPA_DisabledWhenNoStaticDir(t *testing.T) {
	h := New(stubPinger{}, "")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/some/spa/route", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 with static serving disabled, got %d", rec.Code)
	}
}
