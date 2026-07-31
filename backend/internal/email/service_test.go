package email

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/mailer"
)

type stubSettings struct {
	cfg mailer.Config
	ok  bool
	err error
}

func (s stubSettings) configFor(context.Context, uuid.UUID) (mailer.Config, bool, error) {
	return s.cfg, s.ok, s.err
}

type recordingSender struct {
	sent []mailer.Message
	err  error
}

func (r *recordingSender) Send(_ mailer.Config, msg mailer.Message) error {
	if r.err != nil {
		return r.err
	}
	r.sent = append(r.sent, msg)
	return nil
}

type stubBrand struct {
	seeds   Seeds
	err     error
	logo    Logo
	logoErr error
}

func (s stubBrand) MailBrandSeeds(context.Context, uuid.UUID) (Seeds, error) {
	return s.seeds, s.err
}

func (s stubBrand) MailLogo(context.Context, uuid.UUID) (Logo, error) {
	return s.logo, s.logoErr
}

func newTestService(sender mailer.Sender, brand brandSource, locale Locale) *Service {
	return &Service{
		settings:      stubSettings{cfg: mailer.Config{Host: "mail.example.org", Port: 25}, ok: true},
		sender:        sender,
		brand:         brand,
		defaultLocale: locale,
	}
}

func TestSendCredentialOfferDeliversBothPartsBranded(t *testing.T) {
	sender := &recordingSender{}
	svc := newTestService(sender, stubBrand{seeds: Seeds{PrimaryColor: "#ba3354"}}, LocaleEN)

	err := svc.SendCredentialOffer(context.Background(), uuid.New(),
		"person@example.org", "Acme BV", "Employee badge", "https://wallet.example.org/claim/abc", "123456")
	if err != nil {
		t.Fatalf("SendCredentialOffer: %v", err)
	}

	if len(sender.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sender.sent))
	}
	msg := sender.sent[0]
	if msg.To != "person@example.org" {
		t.Errorf("to = %q", msg.To)
	}
	if msg.Subject != "Acme BV has issued you a credential: Employee badge" {
		t.Errorf("subject = %q", msg.Subject)
	}
	if msg.TextBody == "" || msg.HTMLBody == "" {
		t.Error("both a text and an HTML part are required on every send")
	}
	if !strings.Contains(msg.HTMLBody, "#ba3354") {
		t.Errorf("the org's primary colour did not reach the mail:\n%s", msg.HTMLBody)
	}
	if !strings.Contains(msg.TextBody, "123456") {
		t.Errorf("the transaction code is missing from the text part:\n%s", msg.TextBody)
	}
}

func TestSendUsesTheDeploymentDefaultLocale(t *testing.T) {
	sender := &recordingSender{}
	svc := newTestService(sender, nil, LocaleNL)

	if err := svc.SendInvitation(context.Background(), uuid.New(),
		"person@example.org", "Acme BV", "https://wallet.example.org/invite/abc"); err != nil {
		t.Fatalf("SendInvitation: %v", err)
	}

	msg := sender.sent[0]
	if msg.Subject != "Je bent uitgenodigd voor Acme BV" {
		t.Errorf("subject = %q, want the Dutch default", msg.Subject)
	}
	if !strings.Contains(msg.HTMLBody, `<html lang="nl"`) {
		t.Errorf("the mail is not marked as Dutch:\n%s", msg.HTMLBody)
	}
}

// An unset locale must still produce a message rather than an empty template
// lookup, so the zero-value service behaves like an English one.
func TestSendWithoutADefaultLocaleFallsBackToEnglish(t *testing.T) {
	sender := &recordingSender{}
	svc := newTestService(sender, nil, "")

	if err := svc.SendSpecimen(context.Background(), uuid.New(), KindSMTPTest, "", "admin@example.org", "Acme BV"); err != nil {
		t.Fatalf("SendSpecimen: %v", err)
	}
	if got := sender.sent[0].Subject; got != "Test e-mail from your Business Wallet" {
		t.Errorf("subject = %q", got)
	}
}

func TestSendPostguardNotificationMailsEveryRecipient(t *testing.T) {
	sender := &recordingSender{}
	svc := newTestService(sender, nil, LocaleEN)

	recipients := []string{"a@example.org", "b@example.org"}
	if err := svc.SendPostguardNotification(context.Background(), uuid.New(), recipients,
		"Acme BV", "", "https://postguard.example/download?uuid=1"); err != nil {
		t.Fatalf("SendPostguardNotification: %v", err)
	}

	if len(sender.sent) != len(recipients) {
		t.Fatalf("sent %d messages, want %d", len(sender.sent), len(recipients))
	}
	for i, msg := range sender.sent {
		if msg.To != recipients[i] {
			t.Errorf("message %d went to %q, want %q", i, msg.To, recipients[i])
		}
	}
}

// Branding is cosmetic; a theme lookup that fails must not stop the mail.
func TestSendFallsBackToTheDefaultPaletteWhenBrandingFails(t *testing.T) {
	sender := &recordingSender{}
	svc := newTestService(sender, stubBrand{err: errors.New("database down")}, LocaleEN)

	if err := svc.SendInvitation(context.Background(), uuid.New(),
		"person@example.org", "Acme BV", "https://wallet.example.org/invite/abc"); err != nil {
		t.Fatalf("SendInvitation: %v", err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sender.sent))
	}
	if !strings.Contains(sender.sent[0].HTMLBody, defaultPrimary) {
		t.Error("the message did not fall back to the default palette")
	}
}

func TestSendReportsNotConfigured(t *testing.T) {
	svc := &Service{settings: stubSettings{ok: false}, sender: &recordingSender{}}

	err := svc.SendInvitation(context.Background(), uuid.New(),
		"person@example.org", "Acme BV", "https://wallet.example.org/invite/abc")
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

// A bad link is a caller bug, and a mail with a dead call to action is worse than
// no mail: the send has to fail loudly instead.
func TestSendRejectsARelativeLink(t *testing.T) {
	sender := &recordingSender{}
	svc := newTestService(sender, nil, LocaleEN)

	err := svc.SendInvitation(context.Background(), uuid.New(), "person@example.org", "Acme BV", "/invite/abc")
	if err == nil {
		t.Fatal("a relative accept URL was sent")
	}
	if len(sender.sent) != 0 {
		t.Error("a message was sent despite the invalid link")
	}
}

type stubTemplates struct {
	tpl Template
	err error
	// calls records the (kind, locale) pairs asked for, so a test can check the
	// send resolved the language it meant to.
	calls []string
}

func (s *stubTemplates) ResolveTemplate(_ context.Context, _ uuid.UUID, kind Kind, locale Locale) (Template, error) {
	s.calls = append(s.calls, string(kind)+"/"+string(locale))
	if s.err != nil {
		return Template{}, s.err
	}
	return s.tpl, nil
}

// The point of the whole editing slice: a tenant's saved copy is what goes out.
func TestSendUsesTheOrganizationsOwnTemplate(t *testing.T) {
	sender := &recordingSender{}
	templates := &stubTemplates{tpl: Template{
		Subject: "A credential from {{orgName}}",
		Blocks: []Block{
			{Type: BlockHeading, Text: "{{credentialName}} is waiting"},
			{Type: BlockButton, Label: "Open your wallet", URL: "{{claimUrl}}"},
			{Type: BlockFooter, Text: "Sent by {{orgName}}."},
		},
	}}
	svc := newTestService(sender, nil, LocaleEN)
	svc.templates = templates

	err := svc.SendCredentialOffer(context.Background(), uuid.New(),
		"person@example.org", "Acme BV", "Employee badge", "https://wallet.example.org/claim/abc", "123456")
	if err != nil {
		t.Fatalf("SendCredentialOffer: %v", err)
	}
	if got := sender.sent[0].Subject; got != "A credential from Acme BV" {
		t.Errorf("subject = %q, want the org's own copy", got)
	}
	if len(templates.calls) != 1 || templates.calls[0] != "credential_offer/en" {
		t.Errorf("template lookups = %v, want one for credential_offer/en", templates.calls)
	}
}

// A template lookup that fails is a database problem, not a reason to drop the
// message: the shipped default still says something correct.
func TestSendFallsBackToTheShippedTemplateWhenTheLookupFails(t *testing.T) {
	sender := &recordingSender{}
	svc := newTestService(sender, nil, LocaleEN)
	svc.templates = &stubTemplates{err: errors.New("database down")}

	if err := svc.SendInvitation(context.Background(), uuid.New(),
		"person@example.org", "Acme BV", "https://wallet.example.org/invite/abc"); err != nil {
		t.Fatalf("SendInvitation: %v", err)
	}
	shipped, _ := DefaultTemplate(KindInvitation, LocaleEN)
	if len(sender.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sender.sent))
	}
	if !strings.Contains(sender.sent[0].Subject, "Acme BV") || sender.sent[0].Subject == "" {
		t.Errorf("subject = %q, want the shipped default rendered", sender.sent[0].Subject)
	}
	if shipped.Subject == "" {
		t.Fatal("the shipped invitation default has no subject")
	}
}

// An admin previews copy before the org has an SMTP server, so a preview must not
// go through the settings lookup at all.
func TestPreviewNeedsNoSMTPConfiguration(t *testing.T) {
	svc := &Service{settings: stubSettings{ok: false}, sender: &recordingSender{}, defaultLocale: LocaleEN}

	body, err := svc.Preview(context.Background(), uuid.New(), KindCredentialOffer, LocaleNL, nil, "Acme BV")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	shipped, _ := DefaultTemplate(KindCredentialOffer, LocaleNL)
	if body.Subject == "" || body.HTMLBody == "" || body.TextBody == "" {
		t.Fatalf("Preview returned an incomplete body: %+v", body)
	}
	if !strings.Contains(body.HTMLBody, `<html lang="nl"`) {
		t.Error("the preview is not marked as Dutch")
	}
	if shipped.Subject == "" {
		t.Fatal("the shipped Dutch credential-offer default has no subject")
	}
}

// The editor previews unsaved edits, so a supplied draft wins over what is stored.
func TestPreviewRendersTheSuppliedDraftInsteadOfTheStoredOne(t *testing.T) {
	svc := newTestService(&recordingSender{}, nil, LocaleEN)
	svc.templates = &stubTemplates{tpl: Template{Subject: "Stored", Blocks: []Block{{Type: BlockHeading, Text: "Stored"}}}}

	draft := Template{Subject: "Draft for {{orgName}}", Blocks: []Block{{Type: BlockHeading, Text: "Draft"}}}
	body, err := svc.Preview(context.Background(), uuid.New(), KindSMTPTest, LocaleEN, &draft, "Acme BV")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if body.Subject != "Draft for Acme BV" {
		t.Errorf("subject = %q, want the draft", body.Subject)
	}
}

// A draft the tenant is still typing is refused as an invalid template, not as an
// internal error, so the editor can show the reason beside the field.
func TestPreviewReportsAnInvalidDraftAsInvalidTemplate(t *testing.T) {
	svc := newTestService(&recordingSender{}, nil, LocaleEN)

	draft := Template{Subject: "Hello", Blocks: []Block{{Type: BlockHeading, Text: "Hello {{nope}}"}}}
	_, err := svc.Preview(context.Background(), uuid.New(), KindSMTPTest, LocaleEN, &draft, "Acme BV")
	if _, ok := errors.AsType[*InvalidTemplateError](err); !ok {
		t.Fatalf("err = %v, want an InvalidTemplateError", err)
	}
}

// A specimen is a real send of one cause, rendered from the org's own copy.
func TestSendSpecimenUsesTheKindsSampleVariables(t *testing.T) {
	sender := &recordingSender{}
	svc := newTestService(sender, nil, LocaleEN)

	if err := svc.SendSpecimen(context.Background(), uuid.New(),
		KindCredentialOffer, LocaleNL, "admin@example.org", "Acme BV"); err != nil {
		t.Fatalf("SendSpecimen: %v", err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sender.sent))
	}
	msg := sender.sent[0]
	if msg.To != "admin@example.org" {
		t.Errorf("to = %q", msg.To)
	}
	sample, _ := SampleVariables(KindCredentialOffer, LocaleNL, "Acme BV")
	if !strings.Contains(msg.TextBody, sample[varCredentialName]) {
		t.Errorf("the sample credential name is missing from the specimen:\n%s", msg.TextBody)
	}
}
