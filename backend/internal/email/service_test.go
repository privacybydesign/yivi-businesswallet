package email

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/mailer"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/mailoauth"
)

type stubSettings struct {
	cfg sendConfig
	ok  bool
	err error
}

func (s stubSettings) configFor(context.Context, uuid.UUID) (sendConfig, bool, error) {
	return s.cfg, s.ok, s.err
}

// stubTokens stands in for the OAuth token source an XOAUTH2 org sends through.
type stubTokens struct {
	token string
	err   error
	// creds records what the service asked a token for.
	creds mailoauth.Credentials
	calls int
}

func (s *stubTokens) Token(_ context.Context, creds mailoauth.Credentials) (string, error) {
	s.creds = creds
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	return s.token, nil
}

type recordingSender struct {
	sent []mailer.Message
	// configs records the wire config each message went out with, so a test can
	// assert on the credential a send presented.
	configs []mailer.Config
	err     error
	// reject fails the send to one address, the way a relay rejects a mailbox that no
	// longer exists while the rest of the list is deliverable.
	reject map[string]error
}

func (r *recordingSender) Send(cfg mailer.Config, msg mailer.Message) error {
	if r.err != nil {
		return r.err
	}
	if err := r.reject[msg.To]; err != nil {
		return err
	}
	r.sent = append(r.sent, msg)
	r.configs = append(r.configs, cfg)
	return nil
}

func (r *recordingSender) recipients() []string {
	to := make([]string, 0, len(r.sent))
	for _, msg := range r.sent {
		to = append(to, msg.To)
	}
	return to
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
		settings:      stubSettings{cfg: sendConfig{Mailer: mailer.Config{Host: "mail.example.org", Port: 25}}, ok: true},
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

// The flows that send to a list and act on the error keep stopping at the first
// failure — the caller retries the whole flow, so carrying on would mail the later
// recipients twice. Only the notification path fans out past a failure.
func TestSendPostguardNotificationStopsAtTheFirstFailure(t *testing.T) {
	rejected := errors.New("550 no such mailbox")
	sender := &recordingSender{reject: map[string]error{"b@example.org": rejected}}
	svc := newTestService(sender, nil, LocaleEN)

	err := svc.SendPostguardNotification(context.Background(), uuid.New(),
		[]string{"a@example.org", "b@example.org", "c@example.org"},
		"Acme BV", "", "https://postguard.example/download?uuid=1")
	if !errors.Is(err, rejected) {
		t.Fatalf("SendPostguardNotification = %v, want the rejection", err)
	}
	if got := sender.recipients(); len(got) != 1 || got[0] != "a@example.org" {
		t.Errorf("delivered to %v, want only the recipient before the rejected one", got)
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

// Every admin of an org gets the same language: the deployment's default locale is
// the only preference stored, so that is what names the event.
func TestSendEventNotificationNamesTheEventInTheDeploymentLocale(t *testing.T) {
	sender := &recordingSender{}
	svc := newTestService(sender, nil, LocaleNL)

	admins := []string{"ada@acme.example", "sam@acme.example"}
	err := svc.SendEventNotification(context.Background(), uuid.New(), admins, "Acme BV", EventNotification{
		Action:     "membership.invited",
		Details:    "email: sam@example.org\nrole: member",
		OccurredAt: time.Date(2026, 1, 14, 9, 32, 0, 0, time.UTC),
		AuditURL:   "https://wallet.example.org/acme/audit-log",
	})
	if err != nil {
		t.Fatalf("SendEventNotification: %v", err)
	}

	if len(sender.sent) != len(admins) {
		t.Fatalf("sent %d messages, want %d", len(sender.sent), len(admins))
	}
	msg := sender.sent[0]
	if msg.To != admins[0] {
		t.Errorf("to = %q, want %q", msg.To, admins[0])
	}
	label, _ := EventLabel("membership.invited", LocaleNL)
	if !strings.Contains(msg.Subject, label) {
		t.Errorf("subject = %q, want the Dutch event name %q", msg.Subject, label)
	}
	for _, want := range []string{label, "role: member", "2026-01-14 09:32 UTC", "https://wallet.example.org/acme/audit-log"} {
		if !strings.Contains(msg.TextBody, want) {
			t.Errorf("the body is missing %q:\n%s", want, msg.TextBody)
		}
	}
}

// A notification has no second chance: the dispatcher claims the event off the
// outbox before delivering it and never re-queues it. One rejected mailbox must
// therefore cost its own recipient and not the admins after it, and the rejection
// must still surface in the returned error.
func TestSendEventNotificationDeliversPastARejectedRecipient(t *testing.T) {
	rejected := errors.New("550 no such mailbox")
	sender := &recordingSender{reject: map[string]error{"bob@acme.example": rejected}}
	svc := newTestService(sender, nil, LocaleEN)

	admins := []string{"ada@acme.example", "bob@acme.example", "zoe@acme.example"}
	err := svc.SendEventNotification(context.Background(), uuid.New(), admins, "Acme BV", EventNotification{
		Action:     "membership.invited",
		OccurredAt: time.Date(2026, 1, 14, 9, 32, 0, 0, time.UTC),
		AuditURL:   "https://wallet.example.org/acme/audit-log",
	})
	if !errors.Is(err, rejected) {
		t.Fatalf("SendEventNotification = %v, want the rejection", err)
	}
	if !strings.Contains(err.Error(), "bob@acme.example") {
		t.Errorf("err = %v, want it to name the rejected address", err)
	}

	delivered := sender.recipients()
	if len(delivered) != 2 || delivered[0] != "ada@acme.example" || delivered[1] != "zoe@acme.example" {
		t.Errorf("delivered to %v, want both deliverable admins", delivered)
	}
}

// The details paragraph is the one part of the layout that is optional, so an
// event with nothing to report must not leave a dangling empty line.
func TestSendEventNotificationOmitsEmptyDetails(t *testing.T) {
	sender := &recordingSender{}
	svc := newTestService(sender, nil, LocaleEN)

	err := svc.SendEventNotification(context.Background(), uuid.New(), []string{"ada@acme.example"},
		"Acme BV", EventNotification{
			Action:     "wallet.suspended",
			OccurredAt: time.Now(),
			AuditURL:   "https://wallet.example.org/acme/audit-log",
		})
	if err != nil {
		t.Fatalf("SendEventNotification: %v", err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sender.sent))
	}
	label, _ := EventLabel("wallet.suspended", LocaleEN)
	if !strings.Contains(sender.sent[0].TextBody, label) {
		t.Errorf("the body is missing the event name %q:\n%s", label, sender.sent[0].TextBody)
	}
}

// A mail that named the raw audit action would be worse than a logged gap.
func TestSendEventNotificationRefusesAnUnnamedEvent(t *testing.T) {
	sender := &recordingSender{}
	svc := newTestService(sender, nil, LocaleEN)

	err := svc.SendEventNotification(context.Background(), uuid.New(), []string{"ada@acme.example"},
		"Acme BV", EventNotification{
			Action:     "organization.deleted",
			OccurredAt: time.Now(),
			AuditURL:   "https://wallet.example.org/acme/audit-log",
		})
	if err == nil {
		t.Fatal("SendEventNotification on an unnamed event = nil, want an error")
	}
	if len(sender.sent) != 0 {
		t.Errorf("sent %d messages, want none", len(sender.sent))
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
