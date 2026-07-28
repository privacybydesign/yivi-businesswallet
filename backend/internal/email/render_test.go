package email

import (
	"strings"
	"testing"
)

func offerVars() map[string]string {
	return map[string]string{
		varOrgName:        "Acme BV",
		varCredentialName: "Employee badge",
		varClaimURL:       "https://wallet.example.org/claim/abc",
		varTxCode:         "123456",
	}
}

func renderOffer(t *testing.T, tpl Template, vars map[string]string) Body {
	t.Helper()
	body, err := Render(KindCredentialOffer, LocaleEN, tpl, resolveBrand(Seeds{}), vars)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return body
}

func TestValidateTemplateRejectsUnknownPlaceholder(t *testing.T) {
	tpl, _ := DefaultTemplate(KindInvitation, LocaleEN)
	tpl.Paragraphs = append(tpl.Paragraphs, "Signed, {{inviterName}}")

	err := ValidateTemplate(KindInvitation, tpl)
	if err == nil {
		t.Fatal("a placeholder the kind does not declare was accepted")
	}
	if !strings.Contains(err.Error(), "inviterName") {
		t.Errorf("error does not name the offending placeholder: %v", err)
	}
}

// A typo like "{{ org name }}" does not match the placeholder syntax, so without
// this check it would be delivered to the recipient verbatim.
func TestValidateTemplateRejectsMalformedPlaceholder(t *testing.T) {
	tpl, _ := DefaultTemplate(KindInvitation, LocaleEN)
	tpl.Headline = "Hello {{ org name }}"

	if err := ValidateTemplate(KindInvitation, tpl); err == nil {
		t.Fatal("a malformed placeholder was accepted")
	}
}

func TestValidateTemplateRequiresSubjectHeadlineAndPairedCTA(t *testing.T) {
	base, _ := DefaultTemplate(KindInvitation, LocaleEN)

	blankSubject := base
	blankSubject.Subject = "  "
	if err := ValidateTemplate(KindInvitation, blankSubject); err == nil {
		t.Error("an empty subject was accepted")
	}

	blankHeadline := base
	blankHeadline.Headline = ""
	if err := ValidateTemplate(KindInvitation, blankHeadline); err == nil {
		t.Error("an empty headline was accepted")
	}

	halfCTA := base
	halfCTA.CTAURL = ""
	if err := ValidateTemplate(KindInvitation, halfCTA); err == nil {
		t.Error("a call-to-action label without a URL was accepted")
	}
}

func TestValidateTemplateRejectsUnknownKind(t *testing.T) {
	if err := ValidateTemplate(Kind("nope"), Template{Subject: "s", Headline: "h"}); err == nil {
		t.Fatal("an unknown kind was accepted")
	}
}

func TestRenderEscapesValuesInHTMLAndLeavesTextRaw(t *testing.T) {
	tpl, _ := DefaultTemplate(KindCredentialOffer, LocaleEN)
	vars := offerVars()
	vars[varOrgName] = `Acme & <script>alert("x")</script>`

	body := renderOffer(t, tpl, vars)

	if strings.Contains(body.HTMLBody, "<script>") {
		t.Errorf("the HTML part carries unescaped markup:\n%s", body.HTMLBody)
	}
	if !strings.Contains(body.HTMLBody, "&amp;") || !strings.Contains(body.HTMLBody, "&lt;script&gt;") {
		t.Errorf("the HTML part is not escaped as expected:\n%s", body.HTMLBody)
	}
	if !strings.Contains(body.TextBody, `<script>alert("x")</script>`) {
		t.Errorf("the text part should carry the value as-is:\n%s", body.TextBody)
	}
	if strings.Contains(body.Subject, "\n") {
		t.Errorf("the subject is not a single line: %q", body.Subject)
	}
}

// The template text itself is prose, not markup, so a tenant writing "<b>" gets a
// literal "<b>" rather than bold text.
func TestRenderEscapesTemplateProseToo(t *testing.T) {
	tpl, _ := DefaultTemplate(KindCredentialOffer, LocaleEN)
	tpl.Paragraphs = []string{"<b>{{credentialName}}</b> is ready."}

	body := renderOffer(t, tpl, offerVars())

	if strings.Contains(body.HTMLBody, "<b>") {
		t.Errorf("template prose was rendered as markup:\n%s", body.HTMLBody)
	}
	if !strings.Contains(body.HTMLBody, "&lt;b&gt;") {
		t.Errorf("template prose was not escaped:\n%s", body.HTMLBody)
	}
}

func TestRenderTurnsNewlinesIntoLineBreaks(t *testing.T) {
	tpl, _ := DefaultTemplate(KindPostguardFile, LocaleEN)
	body, err := Render(KindPostguardFile, LocaleEN, tpl, resolveBrand(Seeds{}), map[string]string{
		varOrgName:     "Acme BV",
		varMessage:     "First line\nSecond line",
		varDownloadURL: "https://postguard.example/download?uuid=1",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(body.HTMLBody, "First line<br />Second line") {
		t.Errorf("newlines did not become line breaks:\n%s", body.HTMLBody)
	}
	if !strings.Contains(body.TextBody, "First line\nSecond line") {
		t.Errorf("the text part lost the newline:\n%s", body.TextBody)
	}
}

// An optional value is expressed as a block that references only that variable, so
// an empty value must drop the whole block instead of leaving its label dangling.
func TestRenderDropsBlocksWhoseVariablesAreAllEmpty(t *testing.T) {
	tpl, _ := DefaultTemplate(KindCredentialOffer, LocaleEN)
	vars := offerVars()
	vars[varTxCode] = ""

	body := renderOffer(t, tpl, vars)

	if strings.Contains(body.HTMLBody, "ask for this code") {
		t.Errorf("the transaction-code note survived an empty code:\n%s", body.HTMLBody)
	}
	if strings.Contains(body.TextBody, "ask for this code") {
		t.Errorf("the transaction-code note survived in the text part:\n%s", body.TextBody)
	}

	withCode := renderOffer(t, tpl, offerVars())
	if !strings.Contains(withCode.HTMLBody, "123456") || !strings.Contains(withCode.TextBody, "123456") {
		t.Error("the transaction code is missing when it is set")
	}
}

func TestRenderRejectsNonAbsoluteHTTPURLs(t *testing.T) {
	tpl, _ := DefaultTemplate(KindCredentialOffer, LocaleEN)
	for _, bad := range []string{
		"",
		"/claim/abc",
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"wallet.example.org/claim",
		"https://",
	} {
		vars := offerVars()
		vars[varClaimURL] = bad
		if _, err := Render(KindCredentialOffer, LocaleEN, tpl, resolveBrand(Seeds{}), vars); err == nil {
			t.Errorf("URL %q was accepted", bad)
		}
	}
}

func TestRenderRequiresExactlyTheKindsVariables(t *testing.T) {
	tpl, _ := DefaultTemplate(KindCredentialOffer, LocaleEN)

	missing := offerVars()
	delete(missing, varTxCode)
	if _, err := Render(KindCredentialOffer, LocaleEN, tpl, resolveBrand(Seeds{}), missing); err == nil {
		t.Error("a missing variable was accepted")
	}

	extra := offerVars()
	extra["inviterName"] = "Someone"
	if _, err := Render(KindCredentialOffer, LocaleEN, tpl, resolveBrand(Seeds{}), extra); err == nil {
		t.Error("an undeclared variable was accepted")
	}
}

func TestRenderRejectsAnInvalidTemplate(t *testing.T) {
	tpl, _ := DefaultTemplate(KindCredentialOffer, LocaleEN)
	tpl.Note = "Code: {{secret}}"
	if _, err := Render(KindCredentialOffer, LocaleEN, tpl, resolveBrand(Seeds{}), offerVars()); err == nil {
		t.Fatal("a template with an undeclared placeholder rendered")
	}
}

func TestRenderProducesBothPartsWithTheCallToAction(t *testing.T) {
	tpl, _ := DefaultTemplate(KindCredentialOffer, LocaleEN)
	body := renderOffer(t, tpl, offerVars())

	if strings.TrimSpace(body.Subject) == "" {
		t.Error("the subject is empty")
	}
	for name, part := range map[string]string{"html": body.HTMLBody, "text": body.TextBody} {
		if strings.TrimSpace(part) == "" {
			t.Errorf("the %s part is empty", name)
		}
		if !strings.Contains(part, "https://wallet.example.org/claim/abc") {
			t.Errorf("the %s part does not carry the claim URL:\n%s", name, part)
		}
		if !strings.Contains(part, "Add it to your wallet") {
			t.Errorf("the %s part does not carry the call to action:\n%s", name, part)
		}
	}
}

// The subject line the recipient sees must not change silently: these are the
// subjects this package sent before the copy moved out of Go string literals.
func TestRenderKeepsTheShippedSubjects(t *testing.T) {
	tests := []struct {
		kind Kind
		vars map[string]string
		want string
	}{
		{KindCredentialOffer, offerVars(), "Acme BV has issued you a credential: Employee badge"},
		{KindInvitation, map[string]string{
			varOrgName:   "Acme BV",
			varAcceptURL: "https://wallet.example.org/invite/abc",
		}, "You have been invited to join Acme BV"},
		{KindPostguardFile, map[string]string{
			varOrgName:     "Acme BV",
			varMessage:     "",
			varDownloadURL: "https://postguard.example/download?uuid=1",
		}, "Acme BV has sent you an encrypted file"},
		{KindSMTPTest, map[string]string{varOrgName: "Acme BV"}, "Test e-mail from your Business Wallet"},
	}
	for _, tc := range tests {
		tpl, ok := DefaultTemplate(tc.kind, LocaleEN)
		if !ok {
			t.Fatalf("no default template for %q", tc.kind)
		}
		body, err := Render(tc.kind, LocaleEN, tpl, resolveBrand(Seeds{}), tc.vars)
		if err != nil {
			t.Fatalf("%s: Render: %v", tc.kind, err)
		}
		if body.Subject != tc.want {
			t.Errorf("%s subject = %q, want %q", tc.kind, body.Subject, tc.want)
		}
	}
}
