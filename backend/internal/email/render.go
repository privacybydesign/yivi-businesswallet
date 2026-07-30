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
// or call methods. A template is an ordered layout of typed blocks whose text
// fields are prose with {{name}} placeholders, resolved against the kind's
// declared variables and nothing else. Two consequences worth keeping:
//
//   - An undeclared placeholder is a validation error (at save time, and again at
//     render time), never a silent blank.
//   - Values are escaped per part: HTML-escaped in the HTML alternative, raw in
//     the text alternative. The block text itself is escaped the same way, so
//     tenant prose cannot introduce markup.

// placeholderPattern matches a {{name}} placeholder, tolerating inner spaces.
var placeholderPattern = regexp.MustCompile(`\{\{\s*([A-Za-z][A-Za-z0-9]*)\s*\}\}`)

// maxBlocks caps how many blocks one layout may carry, so the designer's "add
// block" cannot grow a message without bound. Enforced by ValidateTemplate, which
// also holds the shipped defaults to it at package init.
const maxBlocks = 24

// Body is a rendered message: the subject line plus both MIME alternatives. The
// recipient address is the caller's business, so it is not part of this.
type Body struct {
	Subject  string
	HTMLBody string
	TextBody string
}

// ValidateTemplate reports whether the template is sendable: every placeholder is
// a declared variable of the kind, every block carries exactly the fields its
// type has, and the layout says something. Callable without any variable values,
// so it is the check to run when a tenant saves a template.
func ValidateTemplate(kind Kind, tpl Template) error {
	variables, ok := VariablesFor(kind)
	if !ok {
		return fmt.Errorf("unknown mail kind %q", kind)
	}
	allowed := make(map[string]bool, len(variables))
	for _, v := range variables {
		allowed[v.Name] = true
	}

	if err := validatePlaceholders(tpl.Subject, allowed); err != nil {
		return fmt.Errorf("subject: %w", err)
	}
	if err := validatePlaceholders(tpl.Preheader, allowed); err != nil {
		return fmt.Errorf("preheader: %w", err)
	}
	if strings.TrimSpace(tpl.Subject) == "" {
		return fmt.Errorf("subject: must not be empty")
	}

	if len(tpl.Blocks) == 0 {
		return fmt.Errorf("blocks: the layout must have at least one block")
	}
	if len(tpl.Blocks) > maxBlocks {
		return fmt.Errorf("blocks: a layout may have at most %d blocks", maxBlocks)
	}
	saysSomething := false
	for i, blk := range tpl.Blocks {
		if err := validateBlock(blk, allowed, variables); err != nil {
			return fmt.Errorf("blocks[%d]: %w", i, err)
		}
		if blk.Type == BlockHeading || blk.Type == BlockParagraph {
			saysSomething = true
		}
	}
	if !saysSomething {
		return fmt.Errorf("blocks: the layout must have at least one heading or paragraph")
	}
	return nil
}

// validateBlock holds one block to its type's field set. A field set on a block
// type it does not belong to is rejected rather than ignored: silently dropping
// tenant content at render time would be worse than refusing the save.
func validateBlock(blk Block, allowed map[string]bool, variables []Variable) error {
	fields := map[string]string{
		"text":         blk.Text,
		"label":        blk.Label,
		"url":          blk.URL,
		"linkFallback": blk.LinkFallback,
	}
	for field, text := range fields {
		if err := validatePlaceholders(text, allowed); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	}

	requireEmpty := func(names ...string) error {
		for _, name := range names {
			if fields[name] != "" {
				return fmt.Errorf("%s: a %s block does not have this field", name, blk.Type)
			}
		}
		return nil
	}

	switch blk.Type {
	case BlockHeading, BlockParagraph, BlockFooter:
		if err := requireEmpty("label", "url", "linkFallback"); err != nil {
			return err
		}
		if strings.TrimSpace(blk.Text) == "" {
			return fmt.Errorf("text: must not be empty")
		}
	case BlockButton:
		if err := requireEmpty("text"); err != nil {
			return err
		}
		if strings.TrimSpace(blk.Label) == "" {
			return fmt.Errorf("label: must not be empty")
		}
		if err := validateButtonURL(blk.URL, variables); err != nil {
			return fmt.Errorf("url: %w", err)
		}
	case BlockLogo, BlockDivider:
		if err := requireEmpty("text", "label", "url", "linkFallback"); err != nil {
			return err
		}
	default:
		return fmt.Errorf("type: unknown block type %q", blk.Type)
	}
	return nil
}

// validateButtonURL closes the gap between the two shapes a button URL may take.
// The placeholder check above only asks whether a referenced variable is
// declared, and the IsURL check in Render only sees variable *values* — so a
// button whose url is the literal "javascript:alert(1)" would otherwise reach an
// href unexamined. A button URL is therefore exactly one of:
//
//   - a single declared URL variable ("{{claimUrl}}"), whose value Render checks;
//   - an absolute http(s) literal with no placeholders at all.
//
// Anything mixed (a literal with a placeholder spliced into it, a non-URL variable)
// is rejected: it cannot be checked without rendering, and no shipped default needs
// it. Tightening this later is easy; loosening it after tenants have saved templates
// is not.
func validateButtonURL(value string, variables []Variable) error {
	if value == "" {
		return fmt.Errorf("must not be empty")
	}
	if match := placeholderPattern.FindStringSubmatch(value); match != nil {
		if match[0] != value {
			return fmt.Errorf("must be either a single URL variable or an absolute http(s) URL, not a mix")
		}
		for _, v := range variables {
			if v.Name == match[1] {
				if !v.IsURL {
					return fmt.Errorf("variable %q is not a URL variable", v.Name)
				}
				return nil
			}
		}
		// An undeclared placeholder is already reported by validatePlaceholders.
		return nil
	}
	return validateAbsoluteHTTPURL(value)
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
	// The resolved URL is what actually lands in the href, so it is checked here
	// too rather than only in its two source shapes.
	for i, blk := range content.blocks {
		if blk.typ == BlockButton {
			if err := validateAbsoluteHTTPURL(blk.url); err != nil {
				return Body{}, fmt.Errorf("email: kind %q: blocks[%d]: url: %w", kind, i, err)
			}
		}
	}
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

// validateAbsoluteHTTPURL requires a parseable absolute http(s) URL with a host.
// Applied to every URL variable and to the resolved button URL (and, for a
// literal, at save time via validateButtonURL), so a call to action can never
// link to a relative path or a javascript:/data: scheme.
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

// prose is one resolved piece of text, in both renderings.
type prose struct {
	html string
	text string
}

func (p prose) empty() bool { return strings.TrimSpace(p.text) == "" }

// resolvedBlock is one layout block with its placeholders resolved, ready for the
// shell to lay out.
type resolvedBlock struct {
	typ BlockType
	// text is a heading, paragraph or footer block's prose.
	text prose
	// label, url and linkFallback belong to a button block. url is the resolved
	// href, identical in both parts.
	label        prose
	url          string
	linkFallback prose
}

// content is a template with its variables resolved, ready for the shell. Blocks
// whose placeholders all resolved to empty values are already dropped, which is
// how an optional part (the wallet's transaction code, a sender's covering
// message) disappears instead of leaving a dangling label.
type content struct {
	locale    Locale
	subject   string
	preheader string
	// orgName is the sending organization's name; the logo block renders it as the
	// wordmark. Every kind declares orgName, so it is always present.
	orgName prose
	blocks  []resolvedBlock
}

func resolveContent(tpl Template, vars map[string]string) content {
	out := content{
		subject:   singleLine(substituteText(tpl.Subject, vars)),
		preheader: singleLine(substituteText(tpl.Preheader, vars)),
		orgName:   prose{html: escapeToHTML(vars[varOrgName]), text: vars[varOrgName]},
	}
	for _, blk := range tpl.Blocks {
		resolved := resolvedBlock{typ: blk.Type}
		switch blk.Type {
		case BlockHeading, BlockParagraph, BlockFooter:
			resolved.text = resolveProse(blk.Text, vars)
			// A text block whose every placeholder resolved to an empty value is
			// dropped whole: "Your wallet will ask for this code: {{txCode}}" must
			// not be sent when there is no code.
			if resolved.text.empty() {
				continue
			}
		case BlockButton:
			resolved.label = resolveProse(blk.Label, vars)
			resolved.linkFallback = resolveProse(blk.LinkFallback, vars)
			resolved.url = substituteText(blk.URL, vars)
		case BlockLogo, BlockDivider:
			// Nothing to resolve.
		}
		out.blocks = append(out.blocks, resolved)
	}
	// An empty preheader falls back to the layout's first heading, else its first
	// paragraph, so the inbox list never shows a blank line.
	if out.preheader == "" {
		out.preheader = firstProse(out.blocks, BlockHeading)
	}
	if out.preheader == "" {
		out.preheader = firstProse(out.blocks, BlockParagraph)
	}
	return out
}

func firstProse(blocks []resolvedBlock, typ BlockType) string {
	for _, blk := range blocks {
		if blk.typ == typ {
			return singleLine(blk.text.text)
		}
	}
	return ""
}

// resolveProse substitutes both renderings of one text field. A field that
// references at least one placeholder and whose every placeholder resolved to an
// empty value collapses to empty prose, so the shell can omit it.
func resolveProse(text string, vars map[string]string) prose {
	if text == "" {
		return prose{}
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
			return prose{}
		}
	}
	return prose{html: substituteHTML(text, vars), text: substituteText(text, vars)}
}

// substituteText resolves placeholders as-is, for the text/plain alternative.
func substituteText(text string, vars map[string]string) string {
	return placeholderPattern.ReplaceAllStringFunc(text, func(match string) string {
		return vars[placeholderName(match)]
	})
}

// substituteHTML escapes the block text and every substituted value, then turns
// newlines into line breaks. Escaping first is safe because HTML escaping leaves
// the braces of a placeholder untouched.
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
