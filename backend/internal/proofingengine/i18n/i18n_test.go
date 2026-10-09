package i18n

import (
	"testing"
)

func TestResolve(t *testing.T) {
	cases := []struct {
		session, accept, want string
	}{
		{"", "", "en"},
		{"nl", "", "nl"},
		{"nl-NL", "en", "nl"},
		{"NL", "", "nl"},
		{"en", "nl", "en"},
		{"", "nl-BE,nl;q=0.9,en;q=0.8", "nl"},
		{"", "de-DE,de;q=0.9,nl;q=0.5,en;q=0.4", "nl"},
		{"", "en;q=0.2,nl;q=0.8", "nl"},
		{"", "nl;q=0,en", "en"},
		{"de", "nl", "nl"},         // unshipped session language: fall through
		{"not a tag!", "nl", "nl"}, // malformed session language: fall through
		{"", "fr,de", "en"},
		{"", "garbage;;q=", "en"},
	}
	for _, c := range cases {
		if got := Resolve(c.session, c.accept); got != c.want {
			t.Errorf("Resolve(%q, %q) = %q, want %q", c.session, c.accept, got, c.want)
		}
	}
}

func TestValid(t *testing.T) {
	for _, tag := range []string{"en", "nl-NL", "de", "zh-Hant-TW"} {
		if !Valid(tag) {
			t.Errorf("Valid(%q) = false", tag)
		}
	}
	for _, tag := range []string{"", "not a tag!", "e"} {
		if Valid(tag) {
			t.Errorf("Valid(%q) = true", tag)
		}
	}
}
