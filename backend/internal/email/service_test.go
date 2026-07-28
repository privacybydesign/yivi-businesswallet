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
	seeds Seeds
	err   error
}

func (s stubBrand) MailBrandSeeds(context.Context, uuid.UUID) (Seeds, error) {
	return s.seeds, s.err
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

	if err := svc.SendTest(context.Background(), uuid.New(), "admin@example.org", "Acme BV"); err != nil {
		t.Fatalf("SendTest: %v", err)
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
