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

// blockIndex finds the first block of a type, failing the test when the shipped
// layout does not carry one.
func blockIndex(t *testing.T, tpl Template, typ BlockType) int {
	t.Helper()
	for i, blk := range tpl.Blocks {
		if blk.Type == typ {
			return i
		}
	}
	t.Fatalf("no %s block in the template", typ)
	return -1
}

func TestValidateTemplateRejectsUnknownPlaceholder(t *testing.T) {
	tpl, _ := DefaultTemplate(KindInvitation, LocaleEN)
	tpl.Blocks = append(tpl.Blocks, Block{Type: BlockParagraph, Text: "Signed, {{inviterName}}"})

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
	tpl.Blocks[blockIndex(t, tpl, BlockHeading)].Text = "Hello {{ org name }}"

	if err := ValidateTemplate(KindInvitation, tpl); err == nil {
		t.Fatal("a malformed placeholder was accepted")
	}
}

func TestValidateTemplateRequiresSubjectAndBlockContent(t *testing.T) {
	base, _ := DefaultTemplate(KindInvitation, LocaleEN)

	blankSubject := base
	blankSubject.Subject = "  "
	if err := ValidateTemplate(KindInvitation, blankSubject); err == nil {
		t.Error("an empty subject was accepted")
	}

	noBlocks := base
	noBlocks.Blocks = nil
	if err := ValidateTemplate(KindInvitation, noBlocks); err == nil {
		t.Error("a layout with no blocks was accepted")
	}

	blankHeading, _ := DefaultTemplate(KindInvitation, LocaleEN)
	blankHeading.Blocks[blockIndex(t, blankHeading, BlockHeading)].Text = " "
	if err := ValidateTemplate(KindInvitation, blankHeading); err == nil {
		t.Error("a heading block with no text was accepted")
	}

	blankLabel, _ := DefaultTemplate(KindInvitation, LocaleEN)
	blankLabel.Blocks[blockIndex(t, blankLabel, BlockButton)].Label = ""
	if err := ValidateTemplate(KindInvitation, blankLabel); err == nil {
		t.Error("a button block without a label was accepted")
	}

	blankURL, _ := DefaultTemplate(KindInvitation, LocaleEN)
	blankURL.Blocks[blockIndex(t, blankURL, BlockButton)].URL = ""
	if err := ValidateTemplate(KindInvitation, blankURL); err == nil {
		t.Error("a button block without a URL was accepted")
	}
}

// A layout of only decoration (logo, divider, footer) says nothing; a message
// needs at least one heading or paragraph.
func TestValidateTemplateRequiresAHeadingOrParagraph(t *testing.T) {
	tpl := Template{Subject: "s", Blocks: []Block{
		{Type: BlockLogo},
		{Type: BlockDivider},
		{Type: BlockFooter, Text: "Sent by {{orgName}}."},
	}}
	if err := ValidateTemplate(KindSMTPTest, tpl); err == nil {
		t.Fatal("a layout without a heading or paragraph was accepted")
	}
}

func TestValidateTemplateCapsTheBlockCount(t *testing.T) {
	tpl := Template{Subject: "s"}
	for range maxBlocks + 1 {
		tpl.Blocks = append(tpl.Blocks, Block{Type: BlockParagraph, Text: "p"})
	}
	if err := ValidateTemplate(KindSMTPTest, tpl); err == nil {
		t.Fatalf("a layout of %d blocks was accepted", maxBlocks+1)
	}
	tpl.Blocks = tpl.Blocks[:maxBlocks]
	if err := ValidateTemplate(KindSMTPTest, tpl); err != nil {
		t.Fatalf("a layout of exactly %d blocks was rejected: %v", maxBlocks, err)
	}
}

// A field set on a block type it does not belong to is a save mistake, not
// content to drop silently at render time.
func TestValidateTemplateRejectsFieldsOnTheWrongBlockType(t *testing.T) {
	cases := map[string]Block{
		"text on a button":     {Type: BlockButton, Label: "Go", URL: "https://example.org", Text: "stray"},
		"label on a paragraph": {Type: BlockParagraph, Text: "p", Label: "stray"},
		"url on a heading":     {Type: BlockHeading, Text: "h", URL: "https://example.org"},
		"text on a divider":    {Type: BlockDivider, Text: "stray"},
		"label on a logo":      {Type: BlockLogo, Label: "stray"},
	}
	for name, blk := range cases {
		t.Run(name, func(t *testing.T) {
			tpl := Template{Subject: "s", Blocks: []Block{{Type: BlockParagraph, Text: "p"}, blk}}
			if err := ValidateTemplate(KindSMTPTest, tpl); err == nil {
				t.Fatal("a block with a stray field was accepted")
			}
		})
	}
}

func TestValidateTemplateRejectsAnUnknownBlockType(t *testing.T) {
	tpl := Template{Subject: "s", Blocks: []Block{
		{Type: BlockParagraph, Text: "p"},
		{Type: BlockType("gif"), Text: "x"},
	}}
	if err := ValidateTemplate(KindSMTPTest, tpl); err == nil {
		t.Fatal("an unknown block type was accepted")
	}
}

func TestValidateTemplateRejectsUnknownKind(t *testing.T) {
	tpl := Template{Subject: "s", Blocks: []Block{{Type: BlockHeading, Text: "h"}}}
	if err := ValidateTemplate(Kind("nope"), tpl); err == nil {
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

// The block text itself is prose, not markup, so a tenant writing "<b>" gets a
// literal "<b>" rather than bold text.
func TestRenderEscapesTemplateProseToo(t *testing.T) {
	tpl, _ := DefaultTemplate(KindCredentialOffer, LocaleEN)
	tpl.Blocks[blockIndex(t, tpl, BlockParagraph)].Text = "<b>{{credentialName}}</b> is ready."

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

// The placeholder allowlist only covers {{variables}} and the IsURL check only sees
// variable *values*, so a button URL written as a literal would otherwise reach the
// href unexamined — a tenant-authored "javascript:..." would have been delivered.
func TestValidateTemplateRejectsAnUnsafeLiteralButtonURL(t *testing.T) {
	for _, bad := range []string{
		"javascript:alert(document.domain)",
		"data:text/html,<script>alert(1)</script>",
		"/claim/abc",
		"wallet.example.org/claim",
		"https://",
		// A literal with a placeholder spliced in cannot be checked without
		// rendering, so it is rejected rather than trusted.
		"https://wallet.example.org/claim/{{claimUrl}}",
		// A non-URL variable is not checked as a URL, so it may not be the href.
		"{{credentialName}}",
	} {
		t.Run(bad, func(t *testing.T) {
			tpl, _ := DefaultTemplate(KindCredentialOffer, LocaleEN)
			tpl.Blocks[blockIndex(t, tpl, BlockButton)].URL = bad

			if err := ValidateTemplate(KindCredentialOffer, tpl); err == nil {
				t.Fatalf("button URL %q was accepted by ValidateTemplate", bad)
			}
			if _, err := Render(KindCredentialOffer, LocaleEN, tpl, resolveBrand(Seeds{}), offerVars()); err == nil {
				t.Fatalf("button URL %q rendered", bad)
			}
		})
	}
}

func TestValidateTemplateAcceptsBothSafeButtonURLShapes(t *testing.T) {
	for _, good := range []string{"{{claimUrl}}", "https://wallet.example.org/claim", "http://localhost:5173/claim"} {
		t.Run(good, func(t *testing.T) {
			tpl, _ := DefaultTemplate(KindCredentialOffer, LocaleEN)
			tpl.Blocks[blockIndex(t, tpl, BlockButton)].URL = good

			if err := ValidateTemplate(KindCredentialOffer, tpl); err != nil {
				t.Fatalf("button URL %q was rejected: %v", good, err)
			}
			body, err := Render(KindCredentialOffer, LocaleEN, tpl, resolveBrand(Seeds{}), offerVars())
			if err != nil {
				t.Fatalf("button URL %q: Render: %v", good, err)
			}
			want := good
			if good == "{{claimUrl}}" {
				want = offerVars()[varClaimURL]
			}
			if !strings.Contains(body.HTMLBody, want) || !strings.Contains(body.TextBody, want) {
				t.Errorf("button URL %q did not reach both parts", good)
			}
		})
	}
}

// A button label that references a variable collapses when that variable is empty
// (resolveProse). Dropping the button then is right; dropping the link with it
// leaves the recipient no way to act on the message.
func TestRenderKeepsTheBareURLWhenTheButtonLabelCollapses(t *testing.T) {
	tpl, _ := DefaultTemplate(KindCredentialOffer, LocaleEN)
	tpl.Blocks[blockIndex(t, tpl, BlockButton)].Label = "Add {{credentialName}} to your wallet"
	vars := offerVars()
	vars[varCredentialName] = ""

	body := renderOffer(t, tpl, vars)

	claimURL := offerVars()[varClaimURL]
	for name, part := range map[string]string{"html": body.HTMLBody, "text": body.TextBody} {
		if !strings.Contains(part, claimURL) {
			t.Errorf("the %s part lost the call-to-action URL with the label:\n%s", name, part)
		}
	}
	if strings.Contains(body.HTMLBody, "Add  to your wallet") {
		t.Errorf("the collapsed label was rendered anyway:\n%s", body.HTMLBody)
	}
	// Without a button, "Or open this link:" has nothing to be an alternative to.
	if strings.Contains(body.HTMLBody, "Or open this link:") {
		t.Errorf("the bare-link introduction survived without a button:\n%s", body.HTMLBody)
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
	tpl.Blocks = append(tpl.Blocks, Block{Type: BlockParagraph, Text: "Code: {{secret}}"})
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

// The layout is the tenant's: blocks must render in the order they were composed,
// in both parts, or the designer's reordering is decoration.
func TestRenderFollowsTheBlockOrder(t *testing.T) {
	// The explicit preheader keeps the hidden inbox line from repeating the
	// heading at the top of the document, which would defeat the order check.
	tpl := Template{Subject: "Order test", Preheader: "Order preview", Blocks: []Block{
		{Type: BlockParagraph, Text: "First paragraph"},
		{Type: BlockButton, Label: "Act now", URL: "https://wallet.example.org/claim/abc"},
		{Type: BlockHeading, Text: "Heading after the button"},
		{Type: BlockParagraph, Text: "Closing paragraph"},
	}}
	body, err := Render(KindSMTPTest, LocaleEN, tpl, resolveBrand(Seeds{}), map[string]string{varOrgName: "Acme BV"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for name, part := range map[string]string{"html": body.HTMLBody, "text": body.TextBody} {
		order := []string{"First paragraph", "Act now", "Heading after the button", "Closing paragraph"}
		last := -1
		for _, needle := range order {
			at := strings.Index(part, needle)
			if at < 0 {
				t.Fatalf("the %s part is missing %q:\n%s", name, needle, part)
			}
			if at < last {
				t.Errorf("the %s part renders %q out of layout order:\n%s", name, needle, part)
			}
			last = at
		}
	}
}

// The logo block is the org wordmark and the divider is decoration: the wordmark
// must reach both parts, the divider only the HTML one.
func TestRenderLogoAndDividerBlocks(t *testing.T) {
	tpl := Template{Subject: "s", Blocks: []Block{
		{Type: BlockLogo},
		{Type: BlockParagraph, Text: "Body"},
		{Type: BlockDivider},
		{Type: BlockFooter, Text: "Small print"},
	}}
	body, err := Render(KindSMTPTest, LocaleEN, tpl, resolveBrand(Seeds{}), map[string]string{varOrgName: "Acme BV"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(body.HTMLBody, "Acme BV") || !strings.Contains(body.TextBody, "Acme BV") {
		t.Error("the logo block did not render the organization wordmark in both parts")
	}
	if !strings.Contains(body.HTMLBody, "border-top") {
		t.Errorf("the divider did not render a rule:\n%s", body.HTMLBody)
	}
	if !strings.Contains(body.HTMLBody, "Small print") || !strings.Contains(body.TextBody, "Small print") {
		t.Error("the footer block is missing from a part")
	}
}

// An empty preheader falls back to the first heading so the inbox list never
// shows a blank line — and the fallback must follow the layout, not a fixed field.
func TestRenderPreheaderFallsBackToTheFirstHeading(t *testing.T) {
	tpl := Template{Subject: "s", Blocks: []Block{
		{Type: BlockParagraph, Text: "Leading paragraph"},
		{Type: BlockHeading, Text: "The heading"},
	}}
	body, err := Render(KindSMTPTest, LocaleEN, tpl, resolveBrand(Seeds{}), map[string]string{varOrgName: "Acme BV"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(body.HTMLBody, ">The heading</div>") {
		t.Fatalf("missing heading:\n%s", body.HTMLBody)
	}
	// The preheader div is the hidden first element; the heading text must appear
	// in it (before the card renders it again).
	preheaderAt := strings.Index(body.HTMLBody, "The heading")
	cardAt := strings.LastIndex(body.HTMLBody, "The heading")
	if preheaderAt == cardAt {
		t.Errorf("the preheader did not fall back to the heading:\n%s", body.HTMLBody)
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

// DefaultTemplate hands out shared package state; a caller editing the returned
// blocks must not be able to edit the shipped default for everyone after it.
func TestDefaultTemplateReturnsACopy(t *testing.T) {
	tpl, _ := DefaultTemplate(KindInvitation, LocaleEN)
	original := tpl.Blocks[blockIndex(t, tpl, BlockHeading)].Text
	tpl.Blocks[blockIndex(t, tpl, BlockHeading)].Text = "mutated"

	fresh, _ := DefaultTemplate(KindInvitation, LocaleEN)
	if got := fresh.Blocks[blockIndex(t, fresh, BlockHeading)].Text; got != original {
		t.Fatalf("mutating a returned template changed the shipped default: %q", got)
	}
}
