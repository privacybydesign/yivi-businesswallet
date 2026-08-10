package email

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
	// KindEventNotification is the notification channel's message: one wallet event
	// the organization subscribed to, mailed to its admins.
	KindEventNotification Kind = "event_notification"
	// KindSignedDocument delivers a completed co-signed document to its recipient
	// (a natural person) as a PDF attachment, with the org's cover message.
	KindSignedDocument Kind = "signed_document"
	// KindSignatureRequested tells a selected member that a document is waiting for
	// their signature, linking to the signing page.
	KindSignatureRequested Kind = "signature_requested"
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
	varEventName      = "eventName"
	varEventDetails   = "eventDetails"
	varEventTime      = "eventTime"
	varAuditURL       = "auditUrl"
	varDocumentName   = "documentName"
	varSigningURL     = "signingUrl"
)

// Variable is one substitutable value of a kind. URL variables are additionally
// checked to be absolute http(s) before substitution, because they end up in an
// href and a relative or javascript: value would be worse than a missing link. A
// literal button URL gets the same check at save time (validateButtonURL); only a
// URL variable may stand in for one.
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
	KindEventNotification: {
		{Name: varOrgName},
		{Name: varEventName},
		{Name: varEventDetails},
		{Name: varEventTime},
		{Name: varAuditURL, IsURL: true},
	},
	KindSignedDocument: {
		{Name: varOrgName},
		{Name: varMessage},
	},
	KindSignatureRequested: {
		{Name: varOrgName},
		{Name: varDocumentName},
		{Name: varSigningURL, IsURL: true},
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

// BlockType names one kind of building block a template layout is composed of.
// The set is closed: every type renders through the mail-client-safe shell
// (shell.go), which is what keeps a tenant-composed layout deliverable — there is
// no free-form HTML block and never will be.
type BlockType string

const (
	// BlockLogo renders the organization's wordmark. Where (and whether) it
	// appears is the layout's call.
	BlockLogo BlockType = "logo"
	// BlockHeading is a prominent line, rendered larger than body text.
	BlockHeading BlockType = "heading"
	// BlockParagraph is one paragraph of body text.
	BlockParagraph BlockType = "paragraph"
	// BlockButton is the call to action: a button plus the bare URL under it, for
	// clients that will not render the button.
	BlockButton BlockType = "button"
	// BlockDivider is a horizontal rule between blocks.
	BlockDivider BlockType = "divider"
	// BlockFooter is small print under a rule, in muted text.
	BlockFooter BlockType = "footer"
)

// BlockTypes returns every block type, in the order the editor offers them.
func BlockTypes() []BlockType {
	return []BlockType{BlockLogo, BlockHeading, BlockParagraph, BlockButton, BlockDivider, BlockFooter}
}

// Block is one building block of a template layout. Which fields apply depends
// on Type — ValidateTemplate rejects a field set on a block type it does not
// belong to, so a stored block never carries stray content. Text fields may
// reference the kind's variables as {{name}} placeholders.
type Block struct {
	Type BlockType `json:"type"`
	// Text is the prose of a heading, paragraph or footer block.
	Text string `json:"text,omitempty"`
	// Label and URL are a button block's call to action. URL is either a single
	// declared URL variable or an absolute http(s) literal (see validateButtonURL).
	Label string `json:"label,omitempty"`
	URL   string `json:"url,omitempty"`
	// LinkFallback introduces the bare URL printed under the button. Empty means
	// the bare URL stands alone.
	LinkFallback string `json:"linkFallback,omitempty"`
}

// Template is the content of one kind in one locale: the parts a tenant edits.
// It carries no HTML — a template is a subject plus an ordered block layout, and
// the mail-client-safe markup every block renders to is the shell's job (see
// shell.go). Every text field may reference the kind's variables as {{name}}
// placeholders.
type Template struct {
	// Subject is the single-line subject.
	Subject string `json:"subject"`
	// Preheader is the short line clients show next to the subject in the inbox
	// list. Empty falls back to the first heading (or paragraph) of the layout.
	Preheader string `json:"preheader,omitempty"`
	// Blocks is the layout, in reading order.
	Blocks []Block `json:"blocks"`
}

// shellDefaults holds the block content shared by every template of a locale, so
// a kind only spells out what is specific to it: a shipped footer block with no
// text gets the locale's footer, and a shipped button block with no linkFallback
// gets the locale's introduction line.
type shellDefaults struct {
	Footer       string `json:"footer"`
	LinkFallback string `json:"linkFallback"`
}

// defaultsFile is the on-disk shape of templates/defaults.<locale>.json.
type defaultsFile struct {
	Shell     shellDefaults     `json:"shell"`
	Samples   map[string]string `json:"samples"`
	Events    map[string]string `json:"events"`
	Templates map[Kind]Template `json:"templates"`
}

//go:embed templates/defaults.*.json
var defaultTemplatesFS embed.FS

// defaults holds everything the shipped catalogue files carry, per locale.
type defaults struct {
	templates map[Kind]Template
	// samples are the stand-in variable values a preview or a specimen send uses,
	// keyed by variable name. They are copy, so they live in the same per-locale
	// file as the templates rather than in Go.
	samples map[string]string
	// events are the human names of the audit actions a notification mail can be
	// about, keyed by audit action. Same reason as samples: it is copy, and the
	// wording matches the audit log's own labels so a mail and the log read alike.
	events map[string]string
}

// shippedDefaults holds the shipped default of every kind in every locale, with
// the locale's shell footer already applied, plus that locale's sample values.
// Loaded once at init: the files are committed data, so a missing kind, an
// undeclared placeholder or a missing sample is a build-time mistake, and failing
// loudly beats sending a blank mail.
var shippedDefaults = mustLoadDefaults()

func mustLoadDefaults() map[Locale]defaults {
	loaded, err := loadDefaults()
	if err != nil {
		panic(fmt.Sprintf("email: loading default templates: %v", err))
	}
	return loaded
}

func loadDefaults() (map[Locale]defaults, error) {
	out := make(map[Locale]defaults, len(supportedLocales))
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
			for i, blk := range tpl.Blocks {
				if blk.Type == BlockFooter && blk.Text == "" {
					tpl.Blocks[i].Text = file.Shell.Footer
				}
				if blk.Type == BlockButton && blk.LinkFallback == "" {
					tpl.Blocks[i].LinkFallback = file.Shell.LinkFallback
				}
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
		if err := validateSamples(file.Samples); err != nil {
			return nil, fmt.Errorf("%s: samples: %w", name, err)
		}
		if err := validateEventLabels(file.Events); err != nil {
			return nil, fmt.Errorf("%s: events: %w", name, err)
		}
		out[locale] = defaults{templates: byKind, samples: file.Samples, events: file.Events}
	}
	// Which actions have a label is a property of the catalogue, not of a language:
	// a locale missing one would leave that event unnotifiable in that language
	// only, which is exactly the kind of gap nobody notices until it happens.
	for _, locale := range supportedLocales {
		for action := range out[DefaultLocale].events {
			if _, ok := out[locale].events[action]; !ok {
				return nil, fmt.Errorf("defaults.%s.json: events: no label for %q", locale, action)
			}
		}
		for action := range out[locale].events {
			if _, ok := out[DefaultLocale].events[action]; !ok {
				return nil, fmt.Errorf("defaults.%s.json: events: %q has no %s label", locale, action, DefaultLocale)
			}
		}
	}
	return out, nil
}

// validateEventLabels holds the shipped event names to what a subject line needs:
// a name per action, and not a blank one. Which actions need one is the
// notification catalog's call, not this package's — the channel that maps an
// audit action onto a mail owns that check (see internal/emailchannel).
func validateEventLabels(events map[string]string) error {
	if len(events) == 0 {
		return fmt.Errorf("no event labels")
	}
	for action, label := range events {
		if action == "" {
			return fmt.Errorf("an event label has no action")
		}
		if strings.TrimSpace(label) == "" {
			return fmt.Errorf("label for %q is empty", action)
		}
	}
	return nil
}

// validateSamples holds the shipped sample values to the same rules a real send
// obeys: every variable a kind declares needs a stand-in (except orgName, which a
// preview fills with the real organization name), a URL variable's stand-in has to
// be an absolute http(s) URL, and a sample for a variable no kind declares is a
// leftover worth failing on rather than carrying.
func validateSamples(samples map[string]string) error {
	declared := map[string]Variable{}
	for _, kind := range Kinds() {
		variables, _ := VariablesFor(kind)
		for _, v := range variables {
			declared[v.Name] = v
			if v.Name == varOrgName {
				continue
			}
			value, ok := samples[v.Name]
			if !ok || value == "" {
				return fmt.Errorf("no sample value for %q (declared by kind %q)", v.Name, kind)
			}
			if v.IsURL {
				if err := validateAbsoluteHTTPURL(value); err != nil {
					return fmt.Errorf("sample for %q: %w", v.Name, err)
				}
			}
		}
	}
	for name := range samples {
		if _, ok := declared[name]; !ok {
			return fmt.Errorf("sample %q is not a variable of any kind", name)
		}
	}
	return nil
}

// localeDefaults returns the shipped catalogue for a locale, falling back to
// DefaultLocale for a locale that has no shipped copy.
func localeDefaults(locale Locale) defaults {
	shipped, ok := shippedDefaults[locale]
	if !ok {
		return shippedDefaults[DefaultLocale]
	}
	return shipped
}

// DefaultTemplate returns the shipped default for a kind in a locale, falling
// back to DefaultLocale for a locale that has no shipped copy. The second result
// is false only for an unknown kind. The block slice is a copy: the shipped
// defaults are shared package state, and a caller must not be able to edit them
// through the returned value.
func DefaultTemplate(kind Kind, locale Locale) (Template, bool) {
	tpl, ok := localeDefaults(locale).templates[kind]
	if ok {
		blocks := make([]Block, len(tpl.Blocks))
		copy(blocks, tpl.Blocks)
		tpl.Blocks = blocks
	}
	return tpl, ok
}

// EventLabel returns the human name of an audit action for a notification mail,
// falling back to DefaultLocale for a locale that has no shipped copy. The second
// result is false for an action the catalogue has no label for; every locale
// labels the same set of actions, so it does not depend on the locale.
func EventLabel(action string, locale Locale) (string, bool) {
	label, ok := localeDefaults(locale).events[action]
	return label, ok
}

// LabelledEvents returns every audit action the catalogue has a name for, sorted.
// The notification channel uses it to assert at test time that the events an org
// can subscribe to all have mail copy.
func LabelledEvents() []string {
	events := localeDefaults(DefaultLocale).events
	out := make([]string, 0, len(events))
	for action := range events {
		out = append(out, action)
	}
	sort.Strings(out)
	return out
}

// SampleVariables returns stand-in values for every variable a kind declares, for
// a preview or a specimen send. orgName is the real organization name — a preview
// that renamed the tenant would not be showing what a recipient gets. The second
// result is false for an unknown kind.
func SampleVariables(kind Kind, locale Locale, orgName string) (map[string]string, bool) {
	variables, ok := VariablesFor(kind)
	if !ok {
		return nil, false
	}
	samples := localeDefaults(locale).samples
	out := make(map[string]string, len(variables))
	for _, v := range variables {
		if v.Name == varOrgName {
			out[v.Name] = orgName
			continue
		}
		out[v.Name] = samples[v.Name]
	}
	return out, true
}
