package email

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
)

// Rendering a template is deliberately NOT text/template or html/template
// execution: a tenant-editable body must not be able to reach into the data model
// or call methods. A template is prose with {{name}} placeholders, resolved
// against the kind's declared variables and nothing else. Two consequences worth
// keeping:
//
//   - An undeclared placeholder is a validation error (at save time, and again at
//     render time), never a silent blank.
//   - Values are escaped per part: HTML-escaped in the HTML alternative, raw in
//     the text alternative. The template text itself is escaped the same way, so
//     tenant prose cannot introduce markup either.

// placeholderPattern matches a {{name}} placeholder, tolerating inner spaces.
var placeholderPattern = regexp.MustCompile(`\{\{\s*([A-Za-z][A-Za-z0-9]*)\s*\}\}`)

// Body is a rendered message: the subject line plus both MIME alternatives. The
// recipient address is the caller's business, so it is not part of this.
type Body struct {
	Subject  string
	HTMLBody string
	TextBody string
}

// ValidateTemplate reports whether every placeholder in the template is a
// declared variable of the kind, and that the parts a message cannot do without
// are present. Callable without any variable values, so it is the check to run
// when a tenant saves a template.
func ValidateTemplate(kind Kind, tpl Template) error {
	variables, ok := VariablesFor(kind)
	if !ok {
		return fmt.Errorf("unknown mail kind %q", kind)
	}
	allowed := make(map[string]bool, len(variables))
	for _, v := range variables {
		allowed[v.Name] = true
	}

	fields := map[string]string{
		"subject":      tpl.Subject,
		"preheader":    tpl.Preheader,
		"headline":     tpl.Headline,
		"ctaLabel":     tpl.CTALabel,
		"ctaUrl":       tpl.CTAURL,
		"linkFallback": tpl.LinkFallback,
		"note":         tpl.Note,
		"footer":       tpl.Footer,
	}
	for i, p := range tpl.Paragraphs {
		fields[fmt.Sprintf("paragraphs[%d]", i)] = p
	}
	for field, text := range fields {
		if err := validatePlaceholders(text, allowed); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	}

	if strings.TrimSpace(tpl.Subject) == "" {
		return fmt.Errorf("subject: must not be empty")
	}
	if strings.TrimSpace(tpl.Headline) == "" {
		return fmt.Errorf("headline: must not be empty")
	}
	if (tpl.CTALabel == "") != (tpl.CTAURL == "") {
		return fmt.Errorf("ctaLabel and ctaUrl: set both or neither")
	}
	return nil
}

// validatePlaceholders rejects an undeclared placeholder, and any leftover "{{"
// that the placeholder syntax does not cover (a typo like "{{ org name }}" would
// otherwise be delivered literally).
func validatePlaceholders(text string, allowed map[string]bool) error {
	for _, match := range placeholderPattern.FindAllStringSubmatch(text, -1) {
		if !allowed[match[1]] {
			return fmt.Errorf("unknown placeholder %q", match[1])
		}
	}
	if strings.Contains(placeholderPattern.ReplaceAllString(text, ""), "{{") {
		return fmt.Errorf("malformed placeholder")
	}
	return nil
}

// Render composes the branded HTML and plain-text alternatives of one message.
// It validates the template and the supplied variables first: an undeclared
// placeholder, a missing variable, or a URL variable that is not an absolute
// http(s) URL all fail here rather than producing a broken mail. The locale is
// carried into the HTML lang attribute so assistive tech announces the message in
// the right language (WCAG 2.2 SC 3.1.1).
func Render(kind Kind, locale Locale, tpl Template, brand Brand, vars map[string]string) (Body, error) {
	if err := ValidateTemplate(kind, tpl); err != nil {
		return Body{}, fmt.Errorf("email: template for %q: %w", kind, err)
	}
	variables, _ := VariablesFor(kind)
	for _, v := range variables {
		value, ok := vars[v.Name]
		if !ok {
			return Body{}, fmt.Errorf("email: kind %q: missing variable %q", kind, v.Name)
		}
		if v.IsURL {
			if err := validateAbsoluteHTTPURL(value); err != nil {
				return Body{}, fmt.Errorf("email: kind %q: variable %q: %w", kind, v.Name, err)
			}
		}
	}
	// A variable the kind does not declare cannot be referenced by any template,
	// so passing one is a caller mistake worth surfacing rather than ignoring.
	for name := range vars {
		if !declares(variables, name) {
			return Body{}, fmt.Errorf("email: kind %q: undeclared variable %q supplied", kind, name)
		}
	}

	content := resolveContent(tpl, vars)
	content.locale = locale
	return Body{
		Subject:  content.subject,
		HTMLBody: renderHTML(content, brand),
		TextBody: renderText(content),
	}, nil
}

func declares(variables []Variable, name string) bool {
	for _, v := range variables {
		if v.Name == name {
			return true
		}
	}
	return false
}

// validateAbsoluteHTTPURL requires a parseable absolute http(s) URL with a host,
// so a template's call to action can never link to a relative path or a
// javascript:/data: scheme.
func validateAbsoluteHTTPURL(value string) error {
	if value == "" {
		return fmt.Errorf("must not be empty")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("not a URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("must be an absolute http(s) URL")
	}
	if parsed.Host == "" {
		return fmt.Errorf("must have a host")
	}
	return nil
}

// block is one resolved piece of prose, in both renderings.
type block struct {
	html string
	text string
}

// content is a template with its variables resolved, ready for the shell. Blocks
// whose placeholders all resolved to empty values are already dropped, which is
// how an optional part (the wallet's transaction code, a sender's covering
// message) disappears instead of leaving a dangling label.
type content struct {
	locale     Locale
	subject    string
	preheader  string
	headerName string
	headline   block
	paragraphs []block
	ctaLabel   block
	ctaURL     string
	// linkFallback introduces the bare call-to-action URL printed under the button.
	linkFallback block
	note         block
	footer       block
}

func resolveContent(tpl Template, vars map[string]string) content {
	out := content{
		subject:      singleLine(substituteText(tpl.Subject, vars)),
		headline:     resolveBlock(tpl.Headline, vars),
		note:         resolveBlock(tpl.Note, vars),
		footer:       resolveBlock(tpl.Footer, vars),
		ctaLabel:     resolveBlock(tpl.CTALabel, vars),
		linkFallback: resolveBlock(tpl.LinkFallback, vars),
		preheader:    singleLine(substituteText(tpl.Preheader, vars)),
		// Every kind declares orgName, so the shell can always name the sender in
		// the header without the template spending a line on it.
		headerName: escapeToHTML(vars[varOrgName]),
	}
	if out.preheader == "" {
		out.preheader = singleLine(out.headline.text)
	}
	if tpl.CTAURL != "" {
		out.ctaURL = substituteText(tpl.CTAURL, vars)
	}
	for _, p := range tpl.Paragraphs {
		if b := resolveBlock(p, vars); !b.empty() {
			out.paragraphs = append(out.paragraphs, b)
		}
	}
	return out
}

func (b block) empty() bool { return strings.TrimSpace(b.text) == "" }

// resolveBlock substitutes both renderings of one field. A field that references
// at least one placeholder and whose every placeholder resolved to an empty value
// collapses to an empty block, so the shell can omit it: "Your wallet will ask
// for this code: {{txCode}}" must not be sent when there is no code.
func resolveBlock(text string, vars map[string]string) block {
	if text == "" {
		return block{}
	}
	names := placeholderPattern.FindAllStringSubmatch(text, -1)
	if len(names) > 0 {
		allEmpty := true
		for _, match := range names {
			if vars[match[1]] != "" {
				allEmpty = false
				break
			}
		}
		if allEmpty {
			return block{}
		}
	}
	return block{html: substituteHTML(text, vars), text: substituteText(text, vars)}
}

// substituteText resolves placeholders as-is, for the text/plain alternative.
func substituteText(text string, vars map[string]string) string {
	return placeholderPattern.ReplaceAllStringFunc(text, func(match string) string {
		return vars[placeholderName(match)]
	})
}

// substituteHTML escapes the template text and every substituted value, then
// turns newlines into line breaks. Escaping first is safe because HTML escaping
// leaves the braces of a placeholder untouched.
func substituteHTML(text string, vars map[string]string) string {
	escaped := escapeToHTML(text)
	return placeholderPattern.ReplaceAllStringFunc(escaped, func(match string) string {
		return escapeToHTML(vars[placeholderName(match)])
	})
}

func escapeToHTML(text string) string {
	return strings.ReplaceAll(html.EscapeString(text), "\n", "<br />")
}

func placeholderName(match string) string {
	return placeholderPattern.FindStringSubmatch(match)[1]
}

// singleLine collapses whitespace so a subject or preheader stays one line. The
// mailer already strips CR/LF from headers; this keeps a multi-line value from
// arriving as a run-together subject.
func singleLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
