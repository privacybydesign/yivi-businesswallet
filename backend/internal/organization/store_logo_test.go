package organization

import (
	"testing"
	"time"
)

func TestLogoURL(t *testing.T) {
	updated := time.Unix(1_700_000_000, 0)

	tests := []struct {
		name      string
		slug      string
		hasLogo   bool
		updatedAt *time.Time
		want      string
	}{
		{
			name:    "no logo yields empty path",
			slug:    "acme",
			hasLogo: false,
			want:    "",
		},
		{
			name:      "logo with timestamp is versioned",
			slug:      "acme",
			hasLogo:   true,
			updatedAt: &updated,
			want:      "/api/v1/orgs/acme/theme/logo?v=1700000000",
		},
		{
			name:    "logo without timestamp falls back to version 0",
			slug:    "acme",
			hasLogo: true,
			want:    "/api/v1/orgs/acme/theme/logo?v=0",
		},
		{
			name:      "slug is path-escaped",
			slug:      "acme corp/eu",
			hasLogo:   true,
			updatedAt: &updated,
			want:      "/api/v1/orgs/acme%20corp%2Feu/theme/logo?v=1700000000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := logoURL(tt.slug, tt.hasLogo, tt.updatedAt); got != tt.want {
				t.Errorf("logoURL(%q, %v, %v) = %q, want %q", tt.slug, tt.hasLogo, tt.updatedAt, got, tt.want)
			}
		})
	}
}
