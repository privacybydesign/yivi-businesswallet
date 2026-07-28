package email

import (
	"regexp"
	"strings"
	"testing"
)

func renderShell(t *testing.T, locale Locale, seeds Seeds) Body {
	t.Helper()
	tpl, ok := DefaultTemplate(KindCredentialOffer, locale)
	if !ok {
		t.Fatalf("no default template for locale %q", locale)
	}
	body, err := Render(KindCredentialOffer, locale, tpl, resolveBrand(seeds), offerVars())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return body
}

// Gmail strips <style>, Outlook ignores most modern CSS, and no client resolves
// the app's --yb-* custom properties. Everything the shell emits therefore has to
// be inline and table-based; these are the mistakes that would look fine in a
// browser preview and break in a real inbox.
func TestShellStaysWithinTheMailClientBaseline(t *testing.T) {
	html := renderShell(t, LocaleEN, Seeds{PrimaryColor: "#ba3354", SurfaceColor: "#f0eeec"}).HTMLBody

	for _, forbidden := range []string{
		"<style", "</style>", "class=", "--yb-", "@media", "var(", "display:flex", "display:grid",
	} {
		if strings.Contains(html, forbidden) {
			t.Errorf("the mail HTML contains %q, which mail clients do not support:\n%s", forbidden, html)
		}
	}
	for _, required := range []string{
		"<!DOCTYPE html>",
		`<table role="presentation"`,
		`cellpadding="0" cellspacing="0" border="0"`,
		"max-width:600px",
		`bgcolor="`,
	} {
		if !strings.Contains(html, required) {
			t.Errorf("the mail HTML is missing %q:\n%s", required, html)
		}
	}
}

// The layout tables carry no data, so every one of them needs
// role="presentation" or a screen reader announces the message as a table grid.
func TestShellMarksEveryTableAsPresentational(t *testing.T) {
	html := renderShell(t, LocaleEN, Seeds{}).HTMLBody

	tables := regexp.MustCompile(`<table[^>]*>`).FindAllString(html, -1)
	if len(tables) == 0 {
		t.Fatal("no tables found in the mail HTML")
	}
	for _, tag := range tables {
		if !strings.Contains(tag, `role="presentation"`) {
			t.Errorf("layout table without role=presentation: %s", tag)
		}
	}
}

func TestShellCarriesTheLocaleAsTheLangAttribute(t *testing.T) {
	for _, locale := range Locales() {
		html := renderShell(t, locale, Seeds{}).HTMLBody
		if want := `<html lang="` + string(locale) + `"`; !strings.Contains(html, want) {
			t.Errorf("locale %q: missing %q", locale, want)
		}
	}
}

func TestShellAppliesTheOrgPalette(t *testing.T) {
	seeds := Seeds{
		PrimaryColor: "#ba3354",
		SurfaceColor: "#f0eeec",
		BorderColor:  "#d0cbc8",
		LinkColor:    "#1d4e89",
		FontFamily:   "Inter, Arial, sans-serif",
	}
	brand := resolveBrand(seeds)
	html := renderShell(t, LocaleEN, seeds).HTMLBody

	for _, want := range []string{brand.Button, brand.Page, brand.Border, brand.FontFamily, brand.ButtonText} {
		if !strings.Contains(html, want) {
			t.Errorf("the mail HTML does not carry %q:\n%s", want, html)
		}
	}
}

// The default palette must reach the mail unchanged for an org that never opened
// theme settings, so unbranded mail still looks like the Yivi wallet.
func TestShellFallsBackToTheDefaultPalette(t *testing.T) {
	html := renderShell(t, LocaleEN, Seeds{}).HTMLBody

	for _, want := range []string{defaultPrimary, defaultSurface, defaultBorder, defaultFontFamily} {
		if !strings.Contains(html, want) {
			t.Errorf("the default palette value %q is missing from the mail HTML", want)
		}
	}
}

func TestShellIncludesAHiddenPreheaderAndABareLinkFallback(t *testing.T) {
	body := renderShell(t, LocaleEN, Seeds{})

	if !strings.Contains(body.HTMLBody, "display:none;max-height:0;overflow:hidden") {
		t.Errorf("the preheader is not hidden from the message body:\n%s", body.HTMLBody)
	}
	if !strings.Contains(body.HTMLBody, "Or open this link:") {
		t.Errorf("the bare-link fallback is missing:\n%s", body.HTMLBody)
	}
	// The button is a link; a client that refuses to render it still leaves the URL.
	if strings.Count(body.HTMLBody, offerVars()[varClaimURL]) < 2 {
		t.Errorf("the call-to-action URL appears only once (button, no fallback):\n%s", body.HTMLBody)
	}
}

func TestShellTextPartIsPlainAndOrdered(t *testing.T) {
	text := renderShell(t, LocaleEN, Seeds{}).TextBody

	if strings.Contains(text, "<") || strings.Contains(text, "&amp;") {
		t.Errorf("the text part carries markup or escapes:\n%s", text)
	}
	headline := strings.Index(text, "has issued you a credential")
	cta := strings.Index(text, "Add it to your wallet")
	footer := strings.Index(text, "Sent by")
	if headline < 0 || cta < 0 || footer < 0 || headline >= cta || cta >= footer {
		t.Errorf("the text part is out of order (headline=%d cta=%d footer=%d):\n%s", headline, cta, footer, text)
	}
}
