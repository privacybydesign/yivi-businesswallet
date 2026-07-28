package email

import (
	"strings"
	"testing"
)

// sampleVars is one valid variable set per kind, for tests that need to render
// every kind.
func sampleVars(kind Kind) map[string]string {
	switch kind {
	case KindCredentialOffer:
		return offerVars()
	case KindInvitation:
		return map[string]string{
			varOrgName:   "Acme BV",
			varAcceptURL: "https://wallet.example.org/invite/abc",
		}
	case KindPostguardFile:
		return map[string]string{
			varOrgName:     "Acme BV",
			varMessage:     "Here is the contract.",
			varDownloadURL: "https://postguard.example/download?uuid=1",
		}
	case KindSMTPTest:
		return map[string]string{varOrgName: "Acme BV"}
	default:
		return nil
	}
}

// Every kind ships copy in every locale. A gap here means a recipient gets an
// English mail from a Dutch org, or a send fails at runtime.
func TestEveryKindHasADefaultTemplatePerLocale(t *testing.T) {
	for _, locale := range Locales() {
		for _, kind := range Kinds() {
			tpl, ok := DefaultTemplate(kind, locale)
			if !ok {
				t.Errorf("locale %q: no default template for kind %q", locale, kind)
				continue
			}
			if err := ValidateTemplate(kind, tpl); err != nil {
				t.Errorf("locale %q kind %q: %v", locale, kind, err)
			}
			if tpl.Footer == "" || tpl.LinkFallback == "" {
				t.Errorf("locale %q kind %q: shell defaults were not applied", locale, kind)
			}
		}
	}
}

// The shipped copy must render for real, not just validate: this catches a
// template that references a variable its kind declares but no caller supplies.
func TestEveryDefaultTemplateRenders(t *testing.T) {
	for _, locale := range Locales() {
		for _, kind := range Kinds() {
			tpl, _ := DefaultTemplate(kind, locale)
			body, err := Render(kind, locale, tpl, resolveBrand(Seeds{}), sampleVars(kind))
			if err != nil {
				t.Errorf("locale %q kind %q: %v", locale, kind, err)
				continue
			}
			if strings.TrimSpace(body.Subject) == "" ||
				strings.TrimSpace(body.HTMLBody) == "" ||
				strings.TrimSpace(body.TextBody) == "" {
				t.Errorf("locale %q kind %q: an empty part was rendered", locale, kind)
			}
		}
	}
}

// The Dutch copy has to actually differ from the English, or the locale plumbing
// is decoration.
func TestDutchDefaultsDifferFromEnglish(t *testing.T) {
	for _, kind := range Kinds() {
		en, _ := DefaultTemplate(kind, LocaleEN)
		nl, _ := DefaultTemplate(kind, LocaleNL)
		if en.Subject == nl.Subject {
			t.Errorf("kind %q: the nl subject is identical to en (%q)", kind, en.Subject)
		}
		if en.Headline == nl.Headline {
			t.Errorf("kind %q: the nl headline is identical to en (%q)", kind, en.Headline)
		}
	}
}

func TestResolveLocalePrefersTheFirstSupportedPreference(t *testing.T) {
	tests := []struct {
		preferences []string
		want        Locale
	}{
		{nil, LocaleEN},
		{[]string{""}, LocaleEN},
		{[]string{"nl"}, LocaleNL},
		{[]string{"", "nl"}, LocaleNL},
		{[]string{"nl", "en"}, LocaleNL},
		{[]string{"de", "nl"}, LocaleNL},
		{[]string{"de"}, LocaleEN},
		{[]string{"NL"}, LocaleEN},
		{[]string{"nl-NL"}, LocaleEN},
	}
	for _, tc := range tests {
		if got := ResolveLocale(tc.preferences...); got != tc.want {
			t.Errorf("ResolveLocale(%q) = %q, want %q", tc.preferences, got, tc.want)
		}
	}
}

func TestDefaultTemplateFallsBackToTheDefaultLocale(t *testing.T) {
	tpl, ok := DefaultTemplate(KindInvitation, Locale("de"))
	if !ok {
		t.Fatal("an unsupported locale returned no template")
	}
	english, _ := DefaultTemplate(KindInvitation, LocaleEN)
	if tpl.Subject != english.Subject {
		t.Errorf("subject = %q, want the English default %q", tpl.Subject, english.Subject)
	}
}

func TestDefaultTemplateRejectsAnUnknownKind(t *testing.T) {
	if _, ok := DefaultTemplate(Kind("nope"), LocaleEN); ok {
		t.Fatal("an unknown kind returned a template")
	}
}

func TestVariablesForDeclaresEveryURLVariable(t *testing.T) {
	urlVars := map[Kind]string{
		KindCredentialOffer: varClaimURL,
		KindInvitation:      varAcceptURL,
		KindPostguardFile:   varDownloadURL,
	}
	for kind, name := range urlVars {
		variables, ok := VariablesFor(kind)
		if !ok {
			t.Fatalf("no variables for kind %q", kind)
		}
		found := false
		for _, v := range variables {
			if v.Name == name {
				found = true
				if !v.IsURL {
					t.Errorf("kind %q: %q is not marked as a URL, so it would skip validation", kind, name)
				}
			}
		}
		if !found {
			t.Errorf("kind %q does not declare %q", kind, name)
		}
	}
	if _, ok := VariablesFor(Kind("nope")); ok {
		t.Error("an unknown kind returned variables")
	}
}

// Every kind's templates may name the organization, and the shell header relies on
// it, so no kind may omit it.
func TestEveryKindDeclaresOrgName(t *testing.T) {
	for _, kind := range Kinds() {
		variables, _ := VariablesFor(kind)
		if !declares(variables, varOrgName) {
			t.Errorf("kind %q does not declare %q", kind, varOrgName)
		}
	}
}
