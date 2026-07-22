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

// SendPostguardNotification notifies each recipient that an organization has sent
// them an encrypted file via PostGuard, linking to the sealed package. Used for
// the PostGuard "own SMTP" delivery path, where the backend mails recipients
// itself instead of PostGuard's hosted service. Returns ErrNotConfigured when the
// org has no usable SMTP settings; on a per-recipient send failure it stops and
// returns that error.
func (s *Service) SendPostguardNotification(ctx context.Context, orgID uuid.UUID, recipients []string, orgName, message, downloadURL string) error {
	cfg, ok, err := s.settings.configFor(ctx, orgID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotConfigured
	}
	subject := fmt.Sprintf("%s has sent you an encrypted file", orgName)
	for _, to := range recipients {
		if err := s.sender.Send(cfg, mailer.Message{
			To:       to,
			Subject:  subject,
			TextBody: postguardText(orgName, message, downloadURL),
			HTMLBody: postguardHTML(orgName, message, downloadURL),
		}); err != nil {
			return err
		}
	}
	return nil
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

func postguardText(orgName, message, downloadURL string) string {
	body := fmt.Sprintf("%s has sent you an encrypted file with PostGuard.\n\n", orgName)
	if message != "" {
		body += fmt.Sprintf("Message:\n%s\n\n", message)
	}
	body += fmt.Sprintf("Open it:\n%s\n\nYou unlock the file by proving ownership of this e-mail address.\n", downloadURL)
	return body
}

func postguardHTML(orgName, message, downloadURL string) string {
	msg := ""
	if message != "" {
		msg = fmt.Sprintf(`<p style="white-space:pre-wrap">%s</p>`, html.EscapeString(message))
	}
	return fmt.Sprintf(
		`<p><strong>%s</strong> has sent you an encrypted file with PostGuard.</p>%s`+
			`<p><a href="%s">Open the file</a></p>`+
			`<p>You unlock the file by proving ownership of this e-mail address.</p>`,
		html.EscapeString(orgName), msg, html.EscapeString(downloadURL),
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
