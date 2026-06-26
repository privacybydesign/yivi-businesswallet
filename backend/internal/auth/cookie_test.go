package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSetSessionCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	cfg := CookieConfig{Secure: true, MaxAge: 3600}

	setSessionCookie(rec, "raw-token", cfg)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if c.Name != cookieName {
		t.Errorf("name = %q, want %q", c.Name, cookieName)
	}
	if c.Value != "raw-token" {
		t.Errorf("value = %q, want raw-token", c.Value)
	}
	if !c.HttpOnly {
		t.Error("HttpOnly must be set")
	}
	if !c.Secure {
		t.Error("Secure must follow cfg")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, want /", c.Path)
	}
	if c.Domain != "" {
		t.Errorf("Domain = %q, want empty (host-scoped)", c.Domain)
	}
	if c.MaxAge != 3600 {
		t.Errorf("MaxAge = %d, want 3600", c.MaxAge)
	}
}

func TestClearSessionCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	cfg := CookieConfig{Secure: true, MaxAge: 3600}

	clearSessionCookie(rec, cfg)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if c.MaxAge >= 0 {
		t.Errorf("MaxAge = %d, want negative (expire now)", c.MaxAge)
	}
	if c.Value != "" {
		t.Errorf("value = %q, want empty", c.Value)
	}
	// Secure/HttpOnly/SameSite/Path/Name must match set, or the browser won't
	// overwrite the original cookie.
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Name != cookieName {
		t.Errorf("clear attributes must match set: %+v", c)
	}
}

func TestReadSessionCookie(t *testing.T) {
	tests := []struct {
		name      string
		setCookie *http.Cookie
		wantRaw   string
		wantOK    bool
	}{
		{"present", &http.Cookie{Name: cookieName, Value: "tok"}, "tok", true},
		{"absent", nil, "", false},
		{"present but empty", &http.Cookie{Name: cookieName, Value: ""}, "", false},
		{"wrong name", &http.Cookie{Name: "other", Value: "tok"}, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.setCookie != nil {
				req.AddCookie(tt.setCookie)
			}
			raw, ok := readSessionCookie(req)
			if ok != tt.wantOK || raw != tt.wantRaw {
				t.Fatalf("got (%q, %v), want (%q, %v)", raw, ok, tt.wantRaw, tt.wantOK)
			}
		})
	}
}
