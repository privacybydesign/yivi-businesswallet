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
// Because the shell owns all markup, a tenant composing a layout composes typed
// blocks only: every block type renders through the same fixed markup, so there
// is no way for tenant input to break the layout, and the text/plain alternative
// below is generated from the same resolved content, so the two parts can never
// say different things.

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
	// logoHeight bounds the rendered logo; width follows the image's aspect ratio.
	// A header logo reads at roughly this height in the app chrome too.
	logoHeight = 40
)

// logoContentID keys the org logo as an inline MIME part; the HTML references it as
// cid:orglogo and the transport attaches it under Content-ID: <orglogo>.
const logoContentID = "orglogo"

// blockSpacing is the vertical gap above each block type, tuned so a heading
// reads as a section start and a button gets room to be a target. The first
// block of the layout gets none.
func blockSpacing(typ BlockType) string {
	switch typ {
	case BlockHeading:
		return "20px"
	case BlockButton, BlockFooter:
		return "24px"
	case BlockDivider, BlockLogo:
		return "20px"
	default: // BlockParagraph
		return "16px"
	}
}

// renderHTML wraps the resolved content in the branded shell: the card, one row
// per resolved block.
func renderHTML(c content, b Brand) string {
	var body strings.Builder

	for i, blk := range c.blocks {
		spacing := blockSpacing(blk.typ)
		if i == 0 {
			spacing = "0"
		}
		fmt.Fprintf(&body, `<tr><td style="padding:%s 0 0 0;">`, spacing)
		renderBlockHTML(&body, blk, c, b)
		fmt.Fprint(&body, `</td></tr>`)
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
<tr><td style="padding:%s;">
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0">
%s
</table>
</td></tr>
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
		shellPadding,
		body.String(),
	)
}

// renderBlockHTML writes the markup of one resolved block into its table cell.
func renderBlockHTML(body *strings.Builder, blk resolvedBlock, c content, b Brand) {
	switch blk.typ {
	case BlockLogo:
		// The organization's uploaded logo, embedded as an inline image (cid:) so it
		// renders in clients that block remote images and offline. The org name is the
		// alt text, so a client with images off still shows who sent the message. When
		// no logo is set the block falls back to the org name as a text wordmark.
		if b.Logo.present() {
			fmt.Fprintf(body,
				`<img src="cid:%s" alt="%s" height="%d" style="display:block;height:%dpx;width:auto;max-width:100%%;border:0;outline:none;text-decoration:none;" />`,
				logoContentID, attr(c.orgName.text), logoHeight, logoHeight,
			)
		} else {
			fmt.Fprintf(body,
				`<div style="font-family:%s;font-size:%s;line-height:%s;font-weight:600;color:%s;">%s</div>`,
				attr(b.FontFamily), smallFontSize, smallLineHeight, attr(b.Muted), c.orgName.html,
			)
		}
	case BlockHeading:
		fmt.Fprintf(body,
			`<div style="font-family:%s;font-size:20px;line-height:28px;font-weight:600;color:%s;">%s</div>`,
			attr(b.FontFamily), attr(b.Text), blk.text.html,
		)
	case BlockParagraph:
		fmt.Fprintf(body,
			`<div style="font-family:%s;font-size:%s;line-height:%s;color:%s;">%s</div>`,
			attr(b.FontFamily), bodyFontSize, bodyLineHeight, attr(b.Text), blk.text.html,
		)
	case BlockButton:
		// The call to action hangs off the URL alone, not off the label. A label
		// that references a variable can collapse at render time (resolveProse), and
		// dropping the whole block then would send a message with no way to act on
		// it — so the button goes and the bare URL stays.
		href := attr(blk.url)
		linkPrefix := ""
		linkMargin := "0"
		if !blk.label.empty() {
			fmt.Fprintf(body,
				`<table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>`+
					`<td bgcolor="%s" style="background-color:%s;border-radius:%s;">`+
					`<a href="%s" style="display:inline-block;padding:12px 22px;font-family:%s;font-size:%s;line-height:%s;font-weight:600;color:%s;text-decoration:none;">%s</a>`+
					`</td></tr></table>`,
				attr(b.Button), attr(b.Button), buttonRadius,
				href, attr(b.FontFamily), bodyFontSize, bodyLineHeight, attr(b.ButtonText), blk.label.html,
			)
			// The introduction ("Or open this link:") only reads right as an
			// alternative to a button; without one the bare link stands alone.
			if !blk.linkFallback.empty() {
				linkPrefix = blk.linkFallback.html + "<br />"
			}
			linkMargin = "12px 0 0 0"
		}
		// The bare URL below the button is what makes the mail usable when a client
		// refuses to render the button, or when the recipient wants to see where the
		// link goes before following it.
		fmt.Fprintf(body,
			`<p style="margin:%s;font-family:%s;font-size:%s;line-height:%s;color:%s;">%s<a href="%s" style="color:%s;">%s</a></p>`,
			linkMargin, attr(b.FontFamily), smallFontSize, smallLineHeight, attr(b.Muted), linkPrefix,
			href, attr(b.Link), html.EscapeString(blk.url),
		)
	case BlockDivider:
		fmt.Fprintf(body, `<div style="border-top:1px solid %s;font-size:1px;line-height:1px;">&nbsp;</div>`, attr(b.Border))
	case BlockFooter:
		fmt.Fprintf(body,
			`<div style="border-top:1px solid %s;padding-top:16px;font-family:%s;font-size:%s;line-height:%s;color:%s;">%s</div>`,
			attr(b.Border), attr(b.FontFamily), smallFontSize, smallLineHeight, attr(b.Muted), blk.text.html,
		)
	}
}

// renderText renders the plain-text alternative from the same resolved content, so
// a recipient whose client shows text/plain gets the same message. A divider is
// decorative and has no text rendering.
func renderText(c content) string {
	var out strings.Builder
	for _, blk := range c.blocks {
		switch blk.typ {
		case BlockLogo:
			writeParagraph(&out, c.orgName.text)
		case BlockHeading, BlockParagraph, BlockFooter:
			writeParagraph(&out, blk.text.text)
		case BlockButton:
			// Same reasoning as renderBlockHTML: a collapsed label loses its line,
			// not the link.
			if blk.label.empty() {
				writeParagraph(&out, blk.url)
			} else {
				writeParagraph(&out, fmt.Sprintf("%s:\n%s", blk.label.text, blk.url))
			}
		case BlockDivider:
			// Nothing to say.
		}
	}
	return strings.TrimRight(out.String(), "\n") + "\n"
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
