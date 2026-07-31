package user

import (
	"testing"
	"time"
)

func TestAvatarURL(t *testing.T) {
	updated := time.Unix(1_700_000_000, 0)

	cases := map[string]struct {
		updatedAt *time.Time
		want      string
	}{
		"no avatar":     {nil, ""},
		"versioned url": {&updated, "/api/v1/me/avatar?v=1700000000"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := AvatarURL(tc.updatedAt); got != tc.want {
				t.Errorf("AvatarURL = %q, want %q", got, tc.want)
			}
		})
	}
}
