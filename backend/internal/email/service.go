package email

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/mailer"
)

// config is the settings surface the service needs (implemented by *Store).
type config interface {
	configFor(ctx context.Context, orgID uuid.UUID) (mailer.Config, bool, error)
}

// brandSource resolves an organization's presentational branding seeds (its theme
// palette and font) so mail carries the same look as the app. A failure here must
// never block a send: mail without the tenant's palette is a cosmetic loss, mail
// not sent is a broken flow.
type brandSource interface {
	MailBrandSeeds(ctx context.Context, orgID uuid.UUID) (Seeds, error)
}

// Service sends transactional e-mail using an org's resolved SMTP config. It owns
// no message copy: every body comes from the template catalogue (catalog.go),
// rendered into the branded, mail-client-safe shell (shell.go).
type Service struct {
	settings config
	sender   mailer.Sender
	brand    brandSource
	// defaultLocale is the deployment's fallback mail language, used when the
	// recipient's own preference is unknown.
	defaultLocale Locale
}

// NewService builds the mail service. brand may be nil, in which case every
// message renders in the default Yivi palette.
func NewService(settings *Store, sender mailer.Sender, brand brandSource, defaultLocale Locale) *Service {
	return &Service{settings: settings, sender: sender, brand: brand, defaultLocale: defaultLocale}
}

// SendCredentialOffer notifies a natural-person recipient that a credential is
// ready, linking to the claim page. Returns ErrNotConfigured when the org has no
// usable SMTP settings.
func (s *Service) SendCredentialOffer(ctx context.Context, orgID uuid.UUID, to, orgName, credentialName, claimURL, txCode string) error {
	return s.send(ctx, orgID, KindCredentialOffer, []string{to}, map[string]string{
		varOrgName:        orgName,
		varCredentialName: credentialName,
		varClaimURL:       claimURL,
		varTxCode:         txCode,
	})
}

// SendInvitation notifies an invited person that they can join an organization,
// linking to the accept page. Returns ErrNotConfigured when the org has no
// usable SMTP settings.
func (s *Service) SendInvitation(ctx context.Context, orgID uuid.UUID, to, orgName, acceptURL string) error {
	return s.send(ctx, orgID, KindInvitation, []string{to}, map[string]string{
		varOrgName:   orgName,
		varAcceptURL: acceptURL,
	})
}

// SendPostguardNotification notifies each recipient that an organization has sent
// them an encrypted file via PostGuard, linking to the sealed package. Used for
// the PostGuard "own SMTP" delivery path, where the backend mails recipients
// itself instead of PostGuard's hosted service. Returns ErrNotConfigured when the
// org has no usable SMTP settings; on a per-recipient send failure it stops and
// returns that error.
func (s *Service) SendPostguardNotification(ctx context.Context, orgID uuid.UUID, recipients []string, orgName, message, downloadURL string) error {
	return s.send(ctx, orgID, KindPostguardFile, recipients, map[string]string{
		varOrgName:     orgName,
		varMessage:     message,
		varDownloadURL: downloadURL,
	})
}

// SendTest sends a specimen message to verify an org's SMTP configuration. It
// renders through the same catalogue and shell as real mail, so the branded
// layout an admin checks is the one a recipient gets.
func (s *Service) SendTest(ctx context.Context, orgID uuid.UUID, to, orgName string) error {
	return s.send(ctx, orgID, KindSMTPTest, []string{to}, map[string]string{
		varOrgName: orgName,
	})
}

// send resolves the org's SMTP config, locale, template and branding, renders the
// message once and delivers it to every recipient.
func (s *Service) send(ctx context.Context, orgID uuid.UUID, kind Kind, recipients []string, vars map[string]string) error {
	cfg, ok, err := s.settings.configFor(ctx, orgID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotConfigured
	}

	// Per-recipient and per-org language preferences are not stored yet, so the
	// deployment default is the only preference there is; ResolveLocale already
	// takes them in recipient -> org -> en order for when they are (see
	// .ai/features/email-templates.md).
	locale := ResolveLocale(string(s.defaultLocale))
	tpl, ok := DefaultTemplate(kind, locale)
	if !ok {
		return fmt.Errorf("email: no template for kind %q", kind)
	}

	body, err := Render(kind, locale, tpl, s.brandFor(ctx, orgID), vars)
	if err != nil {
		return err
	}

	for _, to := range recipients {
		if err := s.sender.Send(cfg, mailer.Message{
			To:       to,
			Subject:  body.Subject,
			TextBody: body.TextBody,
			HTMLBody: body.HTMLBody,
		}); err != nil {
			return err
		}
	}
	return nil
}

// brandFor resolves the org's palette, falling back to the default Yivi look when
// there is no brand source or it errors: an unbranded mail still delivers.
func (s *Service) brandFor(ctx context.Context, orgID uuid.UUID) Brand {
	if s.brand == nil {
		return resolveBrand(Seeds{})
	}
	seeds, err := s.brand.MailBrandSeeds(ctx, orgID)
	if err != nil {
		slog.WarnContext(ctx, "resolving mail branding failed, sending with the default palette",
			"org_id", orgID, "error", err)
		return resolveBrand(Seeds{})
	}
	return resolveBrand(seeds)
}
