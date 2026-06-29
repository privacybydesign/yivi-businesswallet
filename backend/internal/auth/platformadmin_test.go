package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

func TestPlatformAdminsHas(t *testing.T) {
	admins := NewPlatformAdmins([]string{"Admin@Example.com", "  other@example.com  "})

	cases := map[user.Email]bool{
		"admin@example.com":  true,
		"other@example.com":  true,
		"nobody@example.com": false,
	}
	for email, want := range cases {
		if got := admins.Has(email); got != want {
			t.Errorf("Has(%q) = %v, want %v", email, got, want)
		}
	}
}

func TestRequirePlatformAdmin(t *testing.T) {
	admins := NewPlatformAdmins([]string{"admin@example.com"})
	mw := RequirePlatformAdmin(admins)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	tests := []struct {
		name  string
		email user.Email
		want  int
	}{
		{"platform admin passes", "admin@example.com", http.StatusOK},
		{"non-admin forbidden", "user@example.com", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req = req.WithContext(ContextWithUser(req.Context(), user.User{ID: uuid.New(), Email: tt.email}))
			rec := httptest.NewRecorder()

			mw(next).ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}
