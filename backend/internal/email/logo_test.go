package email

import (
	"strings"
	"testing"
)

func logoBrand() Brand {
	b := resolveBrand(Seeds{})
	b.Logo = Logo{Bytes: []byte("fake-png-bytes"), ContentType: "image/png"}
	return b
}

// With a logo set, the logo block renders an inline image referenced by cid: and
// the rendered body carries the image to attach.
func TestRenderLogoBlockEmbedsTheLogoImage(t *testing.T) {
	tpl, ok := DefaultTemplate(KindCredentialOffer, LocaleEN)
	if !ok {
		t.Fatal("no default template")
	}
	body, err := Render(KindCredentialOffer, LocaleEN, tpl, logoBrand(), offerVars())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if !strings.Contains(body.HTMLBody, `src="cid:orglogo"`) {
		t.Errorf("logo block did not reference the inline image:\n%s", body.HTMLBody)
	}
	// The org name is the alt text, so an images-off client still shows the sender.
	if !strings.Contains(body.HTMLBody, `alt="`+offerVars()[varOrgName]+`"`) {
		t.Errorf("logo image has no org-name alt text:\n%s", body.HTMLBody)
	}
	if body.InlineLogo == nil {
		t.Fatal("Body.InlineLogo was not set for a layout with a logo block")
	}
	if body.InlineLogo.ContentID != "orglogo" || body.InlineLogo.ContentType != "image/png" {
		t.Errorf("unexpected inline logo metadata: %+v", body.InlineLogo)
	}
}

// With no logo set the block falls back to the org name as a text wordmark, and
// nothing is attached.
func TestRenderLogoBlockFallsBackToWordmark(t *testing.T) {
	tpl, ok := DefaultTemplate(KindCredentialOffer, LocaleEN)
	if !ok {
		t.Fatal("no default template")
	}
	body, err := Render(KindCredentialOffer, LocaleEN, tpl, resolveBrand(Seeds{}), offerVars())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if strings.Contains(body.HTMLBody, "cid:orglogo") {
		t.Errorf("wordmark fallback should not reference an inline image:\n%s", body.HTMLBody)
	}
	if body.InlineLogo != nil {
		t.Error("Body.InlineLogo was set without a logo image")
	}
	if !strings.Contains(body.HTMLBody, offerVars()[varOrgName]) {
		t.Errorf("wordmark fallback is missing the org name:\n%s", body.HTMLBody)
	}
}

// The preview swaps the cid: reference for a data: URI so a sandboxed iframe shows
// the same image, and it carries no attachment.
func TestInlinePreviewLogoRewritesToDataURI(t *testing.T) {
	tpl, ok := DefaultTemplate(KindCredentialOffer, LocaleEN)
	if !ok {
		t.Fatal("no default template")
	}
	body, err := Render(KindCredentialOffer, LocaleEN, tpl, logoBrand(), offerVars())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	preview := inlinePreviewLogo(body)
	if strings.Contains(preview.HTMLBody, "cid:orglogo") {
		t.Errorf("preview still references cid::\n%s", preview.HTMLBody)
	}
	if !strings.Contains(preview.HTMLBody, "src=\"data:image/png;base64,") {
		t.Errorf("preview did not inline the logo as a data URI:\n%s", preview.HTMLBody)
	}
	if preview.InlineLogo != nil {
		t.Error("preview should carry no attachment")
	}
}
