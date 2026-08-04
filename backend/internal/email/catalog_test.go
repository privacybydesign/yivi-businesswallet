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
	case KindEventNotification:
		return map[string]string{
			varOrgName:      "Acme BV",
			varEventName:    "Invited member",
			varEventDetails: "email: sam@example.org\nrole: member",
			varEventTime:    "2026-01-14 09:32 UTC",
			varAuditURL:     "https://wallet.example.org/acme/audit-log",
		}
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
			for i, blk := range tpl.Blocks {
				if blk.Type == BlockFooter && blk.Text == "" {
					t.Errorf("locale %q kind %q: blocks[%d]: the shell footer was not applied", locale, kind, i)
				}
				if blk.Type == BlockButton && blk.LinkFallback == "" {
					t.Errorf("locale %q kind %q: blocks[%d]: the shell linkFallback was not applied", locale, kind, i)
				}
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
		enHeading := en.Blocks[blockIndex(t, en, BlockHeading)].Text
		nlHeading := nl.Blocks[blockIndex(t, nl, BlockHeading)].Text
		if enHeading == nlHeading {
			t.Errorf("kind %q: the nl heading is identical to en (%q)", kind, enHeading)
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
		KindCredentialOffer:   varClaimURL,
		KindInvitation:        varAcceptURL,
		KindPostguardFile:     varDownloadURL,
		KindEventNotification: varAuditURL,
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

// Every kind's sample variables must render its shipped copy, in every locale:
// they are what the editor's preview and the "send me a specimen" button use, so
// a missing or non-absolute sample would only surface as a broken preview.
func TestSampleVariablesRenderEveryKindInEveryLocale(t *testing.T) {
	for _, locale := range Locales() {
		for _, kind := range Kinds() {
			vars, ok := SampleVariables(kind, locale, "Acme BV")
			if !ok {
				t.Errorf("locale %q: no sample variables for kind %q", locale, kind)
				continue
			}
			declared, _ := VariablesFor(kind)
			if len(vars) != len(declared) {
				t.Errorf("locale %q kind %q: %d sample values for %d declared variables", locale, kind, len(vars), len(declared))
			}
			tpl, _ := DefaultTemplate(kind, locale)
			if _, err := Render(kind, locale, tpl, resolveBrand(Seeds{}), vars); err != nil {
				t.Errorf("locale %q kind %q: %v", locale, kind, err)
			}
		}
	}
}

// A preview that renamed the tenant would not be showing what a recipient gets,
// so orgName is the one sample value the caller supplies.
func TestSampleVariablesUseTheRealOrganizationName(t *testing.T) {
	vars, ok := SampleVariables(KindCredentialOffer, LocaleEN, "Acme BV")
	if !ok {
		t.Fatal("SampleVariables reported an unknown kind")
	}
	if vars[varOrgName] != "Acme BV" {
		t.Errorf("orgName = %q, want the real organization name", vars[varOrgName])
	}
	if vars[varCredentialName] == "" {
		t.Error("credentialName has no stand-in value")
	}
}

// Event names are copy, so they live in the catalogue files and follow the same
// locale rules as the templates. Which actions need one is the notification
// catalog's call and is asserted in internal/emailchannel.
func TestEventLabelsAreShippedPerLocale(t *testing.T) {
	events := LabelledEvents()
	if len(events) == 0 {
		t.Fatal("the catalogue names no events")
	}
	for _, action := range events {
		english, ok := EventLabel(action, LocaleEN)
		if !ok || strings.TrimSpace(english) == "" {
			t.Errorf("no English name for %q", action)
		}
		dutch, ok := EventLabel(action, LocaleNL)
		if !ok || strings.TrimSpace(dutch) == "" {
			t.Errorf("no Dutch name for %q", action)
		}
	}
	// An unsupported locale falls back to the shipped English copy, as elsewhere.
	fallback, ok := EventLabel(events[0], Locale("de"))
	if !ok {
		t.Fatalf("an unsupported locale returned no name for %q", events[0])
	}
	if english, _ := EventLabel(events[0], LocaleEN); fallback != english {
		t.Errorf("name = %q, want the English name %q", fallback, english)
	}
}

func TestEventLabelRejectsAnUnnamedAction(t *testing.T) {
	if _, ok := EventLabel("organization.deleted", LocaleEN); ok {
		t.Error("an action outside the notification catalog has a mail name")
	}
}

func TestSampleVariablesRejectsAnUnknownKind(t *testing.T) {
	if _, ok := SampleVariables("not_a_kind", LocaleEN, "Acme BV"); ok {
		t.Error("SampleVariables accepted an unknown kind")
	}
}

// An unsupported locale falls back to the shipped English catalogue rather than
// returning nothing, the same way DefaultTemplate does.
func TestSampleVariablesFallBackForAnUnsupportedLocale(t *testing.T) {
	vars, ok := SampleVariables(KindCredentialOffer, "de", "Acme BV")
	if !ok {
		t.Fatal("SampleVariables reported an unknown kind")
	}
	english, _ := SampleVariables(KindCredentialOffer, LocaleEN, "Acme BV")
	if vars[varCredentialName] != english[varCredentialName] {
		t.Errorf("credentialName = %q, want the English stand-in %q", vars[varCredentialName], english[varCredentialName])
	}
}

// validateSamples is what keeps a leftover or missing stand-in out of the shipped
// files; the loader runs it, so this pins the rules it enforces.
func TestValidateSamplesRejectsGapsAndLeftovers(t *testing.T) {
	complete := map[string]string{
		varCredentialName: "Employee badge",
		varClaimURL:       "https://wallet.example.org/claim/sample",
		varTxCode:         "123456",
		varAcceptURL:      "https://wallet.example.org/invite/sample",
		varMessage:        "Here is the file.",
		varDownloadURL:    "https://postguard.example/download?uuid=sample",
		varEventName:      "Invited member",
		varEventDetails:   "role: member",
		varEventTime:      "2026-01-14 09:32 UTC",
		varAuditURL:       "https://wallet.example.org/acme/audit-log",
	}
	if err := validateSamples(complete); err != nil {
		t.Fatalf("validateSamples on the complete set = %v, want nil", err)
	}

	cases := map[string]func(map[string]string){
		"missing sample":   func(m map[string]string) { delete(m, varTxCode) },
		"empty sample":     func(m map[string]string) { m[varMessage] = "" },
		"relative URL":     func(m map[string]string) { m[varClaimURL] = "/claim/sample" },
		"unknown leftover": func(m map[string]string) { m["inviterName"] = "Ada" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			broken := map[string]string{}
			for k, v := range complete {
				broken[k] = v
			}
			mutate(broken)
			if err := validateSamples(broken); err == nil {
				t.Errorf("validateSamples(%v) = nil, want an error", broken)
			}
		})
	}
}
