package email

import (
	"context"
	"fmt"
	"html"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/mailer"
)

// config is the settings surface the service needs (implemented by *Store).
type config interface {
	configFor(ctx context.Context, orgID uuid.UUID) (mailer.Config, bool, error)
}

// Service sends transactional e-mail using an org's resolved SMTP config.
type Service struct {
	settings config
	sender   mailer.Sender
}

func NewService(settings *Store, sender mailer.Sender) *Service {
	return &Service{settings: settings, sender: sender}
}

// SendCredentialOffer notifies a natural-person recipient that a credential is
// ready, linking to the claim page. Returns ErrNotConfigured when the org has no
// usable SMTP settings.
func (s *Service) SendCredentialOffer(ctx context.Context, orgID uuid.UUID, to, orgName, credentialName, claimURL, txCode string) error {
	cfg, ok, err := s.settings.configFor(ctx, orgID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotConfigured
	}
	subject := fmt.Sprintf("%s has issued you a credential: %s", orgName, credentialName)
	return s.sender.Send(cfg, mailer.Message{
		To:       to,
		Subject:  subject,
		TextBody: offerText(orgName, credentialName, claimURL, txCode),
		HTMLBody: offerHTML(orgName, credentialName, claimURL, txCode),
	})
}

// SendInvitation notifies an invited person that they can join an organization,
// linking to the accept page. Returns ErrNotConfigured when the org has no
// usable SMTP settings.
func (s *Service) SendInvitation(ctx context.Context, orgID uuid.UUID, to, orgName, acceptURL string) error {
	cfg, ok, err := s.settings.configFor(ctx, orgID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotConfigured
	}
	subject := fmt.Sprintf("You have been invited to join %s", orgName)
	return s.sender.Send(cfg, mailer.Message{
		To:       to,
		Subject:  subject,
		TextBody: inviteText(orgName, acceptURL),
		HTMLBody: inviteHTML(orgName, acceptURL),
	})
}

// SendTest sends a minimal message to verify an org's SMTP configuration.
func (s *Service) SendTest(ctx context.Context, orgID uuid.UUID, to string) error {
	cfg, ok, err := s.settings.configFor(ctx, orgID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotConfigured
	}
	return s.sender.Send(cfg, mailer.Message{
		To:       to,
		Subject:  "Test e-mail from your Business Wallet",
		TextBody: "This is a test message confirming your SMTP settings work.",
		HTMLBody: "<p>This is a test message confirming your SMTP settings work.</p>",
	})
}

func offerText(orgName, credentialName, claimURL, txCode string) string {
	body := fmt.Sprintf("%s has issued you a credential: %s.\n\nAdd it to your wallet:\n%s\n", orgName, credentialName, claimURL)
	if txCode != "" {
		body += fmt.Sprintf("\nYour wallet will ask for this code: %s\n", txCode)
	}
	return body
}

func offerHTML(orgName, credentialName, claimURL, txCode string) string {
	code := ""
	if txCode != "" {
		code = fmt.Sprintf(`<p>Your wallet will ask for this code: <strong>%s</strong></p>`, html.EscapeString(txCode))
	}
	return fmt.Sprintf(
		`<p><strong>%s</strong> has issued you a credential: <strong>%s</strong>.</p>`+
			`<p><a href="%s">Add it to your wallet</a></p>%s`,
		html.EscapeString(orgName), html.EscapeString(credentialName), html.EscapeString(claimURL), code,
	)
}

func inviteText(orgName, acceptURL string) string {
	return fmt.Sprintf("You have been invited to join %s on the Business Wallet.\n\nAccept the invitation:\n%s\n", orgName, acceptURL)
}

func inviteHTML(orgName, acceptURL string) string {
	return fmt.Sprintf(
		`<p>You have been invited to join <strong>%s</strong> on the Business Wallet.</p>`+
			`<p><a href="%s">Accept the invitation</a></p>`,
		html.EscapeString(orgName), html.EscapeString(acceptURL),
	)
}
