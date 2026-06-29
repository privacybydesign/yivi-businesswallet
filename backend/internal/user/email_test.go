package user_test

import (
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

func TestParseEmailNormalizes(t *testing.T) {
	cases := map[string]user.Email{
		"user@example.test":     "user@example.test",
		"  User@Example.TEST  ": "user@example.test",
		"MiXeD@CaSe.Com":        "mixed@case.com",
		"Jane Doe <jane@x.io>":  "jane@x.io",
	}
	for raw, want := range cases {
		got, err := user.ParseEmail(raw)
		if err != nil {
			t.Fatalf("ParseEmail(%q): %v", raw, err)
		}
		if got != want {
			t.Errorf("ParseEmail(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestParseEmailRejectsInvalid(t *testing.T) {
	for _, raw := range []string{"", "   ", "not-an-email", "@example.com", "foo@"} {
		if _, err := user.ParseEmail(raw); err == nil {
			t.Errorf("ParseEmail(%q) = nil error, want error", raw)
		}
	}
}
