//go:build integration

package integration

import (
	"net/http"
	"testing"
)

func TestAuthRoundTrip(t *testing.T) {
	env := setup(t)

	// No session yet.
	resp := env.do(http.MethodGet, "/api/v1/me", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /me before login = %d, want 401", resp.StatusCode)
	}

	const email = "alice@example.test"
	claimed := env.login(email)
	if claimed.Email != email {
		t.Errorf("claimed email = %q, want %q", claimed.Email, email)
	}
	if claimed.IsPlatformAdmin {
		t.Errorf("isPlatformAdmin = true, want false for a regular user")
	}

	// The cookie from /claim authenticates /me.
	me := env.getMe(t)
	if me.ID != claimed.ID || me.Email != email {
		t.Errorf("GET /me = %+v, want id %s email %q", me, claimed.ID, email)
	}
}

func TestMeReportsPlatformAdmin(t *testing.T) {
	const adminEmail = "admin@example.test"
	env := setup(t, adminEmail)

	claimed := env.login(adminEmail)
	if !claimed.IsPlatformAdmin {
		t.Errorf("isPlatformAdmin = false, want true for a configured platform admin")
	}
}

func TestLogoutClearsSession(t *testing.T) {
	env := setup(t)
	env.login("alice@example.test")

	resp := env.do(http.MethodPost, "/api/v1/auth/logout", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204", resp.StatusCode)
	}

	after := env.do(http.MethodGet, "/api/v1/me", nil)
	_ = after.Body.Close()
	if after.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /me after logout = %d, want 401", after.StatusCode)
	}
}

func TestHealthEndpoints(t *testing.T) {
	env := setup(t)

	live := env.do(http.MethodGet, "/livez", nil)
	_ = live.Body.Close()
	if live.StatusCode != http.StatusOK {
		t.Errorf("GET /livez = %d, want 200", live.StatusCode)
	}

	ready := env.do(http.MethodGet, "/readyz", nil)
	_ = ready.Body.Close()
	if ready.StatusCode != http.StatusOK {
		t.Errorf("GET /readyz = %d, want 200", ready.StatusCode)
	}
}
