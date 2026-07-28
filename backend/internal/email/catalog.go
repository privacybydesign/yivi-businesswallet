package email

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"sort"
)

// The mail catalogue: one Kind per cause the backend sends mail for, each with a
// closed set of variables it may reference, and a shipped default Template per
// locale. Kinds are enumerated in Go on purpose — adding a cause is a code change
// (a new caller has to supply its variables), while editing a cause's wording is
// data. The shipped defaults live in templates/defaults.<locale>.json rather than
// in Go string literals so the copy has one home and can be diffed as copy.

// Kind identifies one cause the backend sends transactional mail for.
type Kind string

const (
	// KindCredentialOffer is sent when an org issues a credential to a natural
	// person, linking to the claim page.
	KindCredentialOffer Kind = "credential_offer"
	// KindInvitation is sent when an org admin invites someone to join the org.
	KindInvitation Kind = "invitation"
	// KindPostguardFile is sent on the PostGuard "own SMTP" delivery path, linking
	// to the sealed package.
	KindPostguardFile Kind = "postguard_file"
	// KindSMTPTest is the specimen an admin sends to themselves to verify SMTP
	// settings.
	KindSMTPTest Kind = "smtp_test"
)

// Variable names. Every placeholder a template may use is one of these, declared
// per kind below; anything else fails validation instead of rendering blank.
const (
	varOrgName        = "orgName"
	varCredentialName = "credentialName"
	varClaimURL       = "claimUrl"
	varTxCode         = "txCode"
	varAcceptURL      = "acceptUrl"
	varMessage        = "message"
	varDownloadURL    = "downloadUrl"
)

// Variable is one substitutable value of a kind. URL variables are additionally
// checked to be absolute http(s) before substitution, because they end up in an
// href and a relative or javascript: value would be worse than a missing link. A
// literal ctaUrl gets the same check at save time (validateCTAURL); only a URL
// variable may stand in for one.
type Variable struct {
	Name  string
	IsURL bool
}

// kindVariables is the allowlist per kind. A kind's caller supplies exactly these.
var kindVariables = map[Kind][]Variable{
	KindCredentialOffer: {
		{Name: varOrgName},
		{Name: varCredentialName},
		{Name: varClaimURL, IsURL: true},
		{Name: varTxCode},
	},
	KindInvitation: {
		{Name: varOrgName},
		{Name: varAcceptURL, IsURL: true},
	},
	KindPostguardFile: {
		{Name: varOrgName},
		{Name: varMessage},
		{Name: varDownloadURL, IsURL: true},
	},
	KindSMTPTest: {
		{Name: varOrgName},
	},
}

// Kinds returns every mail kind, in a stable order.
func Kinds() []Kind {
	out := make([]Kind, 0, len(kindVariables))
	for k := range kindVariables {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// VariablesFor returns the variables a kind's templates may reference, in the
// order the editor should offer them. The second result is false for an unknown
// kind.
func VariablesFor(kind Kind) ([]Variable, bool) {
	vars, ok := kindVariables[kind]
	if !ok {
		return nil, false
	}
	return vars, true
}

// Locale is a supported mail language, matching the frontend's
// src/i18n/locales/<locale>.ts set.
type Locale string

const (
	// LocaleEN is English.
	LocaleEN Locale = "en"
	// LocaleNL is Dutch.
	LocaleNL Locale = "nl"
)

// DefaultLocale is the last fallback when no preference resolves to a supported
// locale.
const DefaultLocale = LocaleEN

var supportedLocales = []Locale{LocaleEN, LocaleNL}

// Locales returns every supported mail locale, in a stable order.
func Locales() []Locale {
	out := make([]Locale, len(supportedLocales))
	copy(out, supportedLocales)
	return out
}

// ParseLocale accepts a locale tag and reports whether it is supported.
func ParseLocale(tag string) (Locale, bool) {
	for _, l := range supportedLocales {
		if string(l) == tag {
			return l, true
		}
	}
	return "", false
}

// ResolveLocale picks the mail locale from an ordered list of preferences —
// recipient first, then the organization's default — and falls back to
// DefaultLocale when none of them is supported. Empty strings are skipped, so a
// caller that does not know a preference can simply pass "".
func ResolveLocale(preferences ...string) Locale {
	for _, pref := range preferences {
		if l, ok := ParseLocale(pref); ok {
			return l
		}
	}
	return DefaultLocale
}

// Template is the content of one kind in one locale: the parts a tenant edits.
// It carries no HTML — the mail-client-safe layout is the shell's job (see
// shell.go), so a template holds prose plus a call to action. Every field may
// reference the kind's variables as {{name}} placeholders.
type Template struct {
	// Subject is the single-line subject.
	Subject string `json:"subject"`
	// Preheader is the short line clients show next to the subject in the inbox
	// list. Empty falls back to the headline.
	Preheader string `json:"preheader,omitempty"`
	// Headline is the message's opening line, rendered larger than body text.
	Headline string `json:"headline"`
	// Paragraphs are the body paragraphs, in order.
	Paragraphs []string `json:"paragraphs,omitempty"`
	// CTALabel and CTAURL are the call to action. Both empty means no button.
	CTALabel string `json:"ctaLabel,omitempty"`
	CTAURL   string `json:"ctaUrl,omitempty"`
	// LinkFallback introduces the bare call-to-action URL printed under the button,
	// for a client that will not render it. Empty falls back to the locale's shell
	// value.
	LinkFallback string `json:"linkFallback,omitempty"`
	// Note is a closing remark below the call to action.
	Note string `json:"note,omitempty"`
	// Footer is the small print under the rule. Empty falls back to the locale's
	// shell footer.
	Footer string `json:"footer,omitempty"`
}

// shellDefaults holds the shell parts shared by every template of a locale, so a
// kind only spells out what is specific to it.
type shellDefaults struct {
	Footer       string `json:"footer"`
	LinkFallback string `json:"linkFallback"`
}

// defaultsFile is the on-disk shape of templates/defaults.<locale>.json.
type defaultsFile struct {
	Shell     shellDefaults     `json:"shell"`
	Templates map[Kind]Template `json:"templates"`
}

//go:embed templates/defaults.*.json
var defaultTemplatesFS embed.FS

// defaultTemplates holds the shipped default of every kind in every locale, with
// the locale's shell footer already applied. Loaded once at init: the files are
// committed data, so a missing kind or an undeclared placeholder is a build-time
// mistake, and failing loudly beats sending a blank mail.
var defaultTemplates = mustLoadDefaults()

func mustLoadDefaults() map[Locale]map[Kind]Template {
	loaded, err := loadDefaults()
	if err != nil {
		panic(fmt.Sprintf("email: loading default templates: %v", err))
	}
	return loaded
}

func loadDefaults() (map[Locale]map[Kind]Template, error) {
	out := make(map[Locale]map[Kind]Template, len(supportedLocales))
	for _, locale := range supportedLocales {
		name := fmt.Sprintf("templates/defaults.%s.json", locale)
		raw, err := defaultTemplatesFS.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		var file defaultsFile
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&file); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", name, err)
		}
		if file.Shell.Footer == "" || file.Shell.LinkFallback == "" {
			return nil, fmt.Errorf("%s: shell footer and linkFallback must both be set", name)
		}
		byKind := make(map[Kind]Template, len(kindVariables))
		for _, kind := range Kinds() {
			tpl, ok := file.Templates[kind]
			if !ok {
				return nil, fmt.Errorf("%s: no template for kind %q", name, kind)
			}
			if tpl.Footer == "" {
				tpl.Footer = file.Shell.Footer
			}
			if tpl.LinkFallback == "" {
				tpl.LinkFallback = file.Shell.LinkFallback
			}
			if err := ValidateTemplate(kind, tpl); err != nil {
				return nil, fmt.Errorf("%s: kind %q: %w", name, kind, err)
			}
			byKind[kind] = tpl
		}
		for kind := range file.Templates {
			if _, ok := kindVariables[kind]; !ok {
				return nil, fmt.Errorf("%s: unknown kind %q", name, kind)
			}
		}
		out[locale] = byKind
	}
	return out, nil
}

// DefaultTemplate returns the shipped default for a kind in a locale, falling
// back to DefaultLocale for a locale that has no shipped copy. The second result
// is false only for an unknown kind.
func DefaultTemplate(kind Kind, locale Locale) (Template, bool) {
	byKind, ok := defaultTemplates[locale]
	if !ok {
		byKind = defaultTemplates[DefaultLocale]
	}
	tpl, ok := byKind[kind]
	return tpl, ok
}
