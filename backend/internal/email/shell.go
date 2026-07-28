package email

import (
	"fmt"
	"html"
	"strings"
)

// The mail shell: the layout every rendered body is wrapped in, branded from the
// org's theme (see brand.go). It is hand-built HTML on purpose, and it is the only
// HTML in this package.
//
// Mail clients are not browsers. The app's --yb-* custom properties, its
// `:root:root` override block and its `prefers-color-scheme: dark` half all rely
// on a real CSS engine, and Gmail strips <style> entirely. So the shell obeys the
// mail-client baseline instead:
//
//   - table-based layout, no flex/grid, fixed 600px content column;
//   - every declaration inline on the element, no <style>, no classes, no custom
//     properties;
//   - no media queries: `width:100%;max-width:600px` is what makes it fit a phone;
//   - a bgcolor attribute next to each background declaration, for the clients
//     that drop CSS backgrounds on table cells;
//   - one light palette. A dark-mode client either leaves it alone or inverts it,
//     and the palette is chosen (dark text on a near-white card, foregrounds nudged
//     toward black) to survive that. There is no dark override to serve.
//
// Because the shell owns all markup, a tenant editing a template edits prose
// only: there is no way for tenant input to break the layout, and the text/plain
// alternative below is generated from the same resolved content, so the two parts
// can never say different things.

// Layout constants of the content column. The radii are the design system's
// --yb-radius / --yb-radius-sm (8px / 6px). Body text runs 15px rather than the
// app's 14px: mail is read once, often on a phone, with no zoom controls to hand,
// and 600px is the column width every mail client is built around.
const (
	shellWidth      = 600
	shellPadding    = "24px 28px"
	cardRadius      = "8px"
	buttonRadius    = "6px"
	bodyFontSize    = "15px"
	bodyLineHeight  = "24px"
	smallFontSize   = "13px"
	smallLineHeight = "20px"
)

// renderHTML wraps the resolved content in the branded shell.
func renderHTML(c content, b Brand) string {
	var body strings.Builder

	// Header: the sending organization's own name, as a text wordmark. The uploaded
	// theme logo is deliberately not here yet — it needs a delivery path that works
	// for a recipient who is not a member of the org, which is an open decision
	// (see .ai/features/email-templates.md).
	if c.headerName != "" {
		fmt.Fprintf(&body,
			`<tr><td style="padding:24px 28px 0 28px;font-family:%s;font-size:%s;line-height:%s;font-weight:600;color:%s;">%s</td></tr>`,
			attr(b.FontFamily), smallFontSize, smallLineHeight, attr(b.Muted), c.headerName,
		)
	}

	fmt.Fprintf(&body,
		`<tr><td style="padding:12px 28px 0 28px;font-family:%s;font-size:20px;line-height:28px;font-weight:600;color:%s;">%s</td></tr>`,
		attr(b.FontFamily), attr(b.Text), c.headline.html,
	)

	for _, p := range c.paragraphs {
		fmt.Fprintf(&body,
			`<tr><td style="padding:16px 28px 0 28px;font-family:%s;font-size:%s;line-height:%s;color:%s;">%s</td></tr>`,
			attr(b.FontFamily), bodyFontSize, bodyLineHeight, attr(b.Text), p.html,
		)
	}

	if c.ctaURL != "" && !c.ctaLabel.empty() {
		href := attr(c.ctaURL)
		linkPrefix := ""
		if !c.linkFallback.empty() {
			linkPrefix = c.linkFallback.html + "<br />"
		}
		fmt.Fprintf(&body,
			`<tr><td style="padding:24px 28px 0 28px;">`+
				`<table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>`+
				`<td bgcolor="%s" style="background-color:%s;border-radius:%s;">`+
				`<a href="%s" style="display:inline-block;padding:12px 22px;font-family:%s;font-size:%s;line-height:%s;font-weight:600;color:%s;text-decoration:none;">%s</a>`+
				`</td></tr></table>`+
				// The bare URL below the button is what makes the mail usable when a
				// client refuses to render the button, or when the recipient wants to
				// see where the link goes before following it.
				`<p style="margin:12px 0 0 0;font-family:%s;font-size:%s;line-height:%s;color:%s;">%s<a href="%s" style="color:%s;">%s</a></p>`+
				`</td></tr>`,
			attr(b.Button), attr(b.Button), buttonRadius,
			href, attr(b.FontFamily), bodyFontSize, bodyLineHeight, attr(b.ButtonText), c.ctaLabel.html,
			attr(b.FontFamily), smallFontSize, smallLineHeight, attr(b.Muted), linkPrefix,
			href, attr(b.Link), html.EscapeString(c.ctaURL),
		)
	}

	if !c.note.empty() {
		fmt.Fprintf(&body,
			`<tr><td style="padding:20px 28px 0 28px;font-family:%s;font-size:%s;line-height:%s;color:%s;">%s</td></tr>`,
			attr(b.FontFamily), bodyFontSize, bodyLineHeight, attr(b.Text), c.note.html,
		)
	}

	if !c.footer.empty() {
		fmt.Fprintf(&body,
			`<tr><td style="padding:%s;"><div style="border-top:1px solid %s;padding-top:16px;font-family:%s;font-size:%s;line-height:%s;color:%s;">%s</div></td></tr>`,
			shellPadding, attr(b.Border), attr(b.FontFamily), smallFontSize, smallLineHeight, attr(b.Muted), c.footer.html,
		)
	} else {
		fmt.Fprintf(&body, `<tr><td style="padding:%s;"></td></tr>`, shellPadding)
	}

	locale := c.locale
	if locale == "" {
		locale = DefaultLocale
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="%s"><head><meta charset="utf-8" /><meta name="viewport" content="width=device-width, initial-scale=1" /><title>%s</title></head>
<body style="margin:0;padding:0;width:100%%;background-color:%s;">
<div style="display:none;max-height:0;overflow:hidden;font-size:1px;line-height:1px;color:%s;">%s</div>
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" bgcolor="%s" style="background-color:%s;">
<tr><td align="center" style="padding:24px 12px;">
<table role="presentation" width="%d" cellpadding="0" cellspacing="0" border="0" bgcolor="%s" style="width:100%%;max-width:%dpx;background-color:%s;border:1px solid %s;border-radius:%s;">
%s
</table>
</td></tr>
</table>
</body></html>
`,
		attr(string(locale)), html.EscapeString(c.subject),
		attr(b.Page),
		attr(b.Page), html.EscapeString(c.preheader),
		attr(b.Page), attr(b.Page),
		shellWidth, attr(b.Card), shellWidth, attr(b.Card), attr(b.Border), cardRadius,
		body.String(),
	)
}

// renderText renders the plain-text alternative from the same resolved content, so
// a recipient whose client shows text/plain gets the same message.
func renderText(c content) string {
	var out strings.Builder
	writeParagraph(&out, c.headline.text)
	for _, p := range c.paragraphs {
		writeParagraph(&out, p.text)
	}
	if c.ctaURL != "" && !c.ctaLabel.empty() {
		writeParagraph(&out, fmt.Sprintf("%s:\n%s", c.ctaLabel.text, c.ctaURL))
	}
	writeParagraph(&out, c.note.text)
	if !c.footer.empty() {
		// No "-- " signature marker: clients that honour it would fold the footer
		// away, and the footer is where the recipient learns who sent this.
		out.WriteString(strings.TrimSpace(c.footer.text))
		out.WriteString("\n")
	}
	return out.String()
}

func writeParagraph(out *strings.Builder, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	out.WriteString(text)
	out.WriteString("\n\n")
}

// attr escapes a value that goes inside a double-quoted HTML attribute. Colours
// and the font family are already pattern-validated (brand.go) and URLs are
// validated absolute http(s) (render.go), so this is defence in depth rather than
// the only guard.
func attr(value string) string { return html.EscapeString(value) }
