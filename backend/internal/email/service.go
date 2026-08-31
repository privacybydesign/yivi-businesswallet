package email

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/mailer"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/mailoauth"
)

// sendConfig is everything one send needs: the SMTP wire config, plus the app
// registration an XOAUTH2 org mints its bearer token from. The two are kept
// apart because the token is resolved per send and never stored — internal/mailer
// only ever sees the token, never the credentials behind it.
type sendConfig struct {
	Mailer mailer.Config
	OAuth  mailoauth.Credentials
}

// config is the settings surface the service needs (implemented by *Store).
type config interface {
	configFor(ctx context.Context, orgID uuid.UUID) (sendConfig, bool, error)
}

// tokenSource mints the OAuth2 bearer token an XOAUTH2 send authenticates with
// (implemented by *mailoauth.Microsoft). It is resolved here rather than in
// internal/mailer because net/smtp takes no context: a token fetch inside Send
// could not be bounded by the caller's deadline.
type tokenSource interface {
	Token(ctx context.Context, creds mailoauth.Credentials) (string, error)
}

// brandSource resolves an organization's presentational branding seeds (its theme
// palette and font) so mail carries the same look as the app. A failure here must
// never block a send: mail without the tenant's palette is a cosmetic loss, mail
// not sent is a broken flow.
type brandSource interface {
	MailBrandSeeds(ctx context.Context, orgID uuid.UUID) (Seeds, error)
	// MailLogo resolves the org's uploaded logo image, or an empty Logo when none is
	// set. Like the palette, a failure here is cosmetic and must not block a send.
	MailLogo(ctx context.Context, orgID uuid.UUID) (Logo, error)
}

// templateSource resolves the template a send uses: the organization's own edit
// when it has one, the shipped default otherwise (implemented by *Store). Like
// branding, a lookup failure falls back to the shipped copy rather than dropping
// the message.
type templateSource interface {
	ResolveTemplate(ctx context.Context, orgID uuid.UUID, kind Kind, locale Locale) (Template, error)
}

// Service sends transactional e-mail using an org's resolved SMTP config. It owns
// no message copy: every body comes from the template catalogue (catalog.go),
// rendered into the branded, mail-client-safe shell (shell.go).
type Service struct {
	settings  config
	sender    mailer.Sender
	tokens    tokenSource
	brand     brandSource
	templates templateSource
	// defaultLocale is the deployment's fallback mail language, used when the
	// recipient's own preference is unknown.
	defaultLocale Locale
}

// NewService builds the mail service. brand may be nil, in which case every
// message renders in the default Yivi palette. tokens may be nil, in which case
// an org configured for XOAUTH2 cannot send — a deployment that leaves it out has
// no OAuth path at all, and failing the send says so rather than falling back to
// a password the tenant does not have. The settings store doubles as the template
// source, so a tenant's edited copy is what a send renders.
func NewService(settings *Store, sender mailer.Sender, tokens tokenSource, brand brandSource, defaultLocale Locale) *Service {
	return &Service{settings: settings, sender: sender, tokens: tokens, brand: brand, templates: settings, defaultLocale: defaultLocale}
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

// EventNotification is one recorded wallet event as the notification mail renders
// it. Action is the audit action, which the catalogue turns into the deployment's
// locale (EventLabel); Details is the already-summarised description of what
// changed, one "field: value" per line, and may be empty, in which case the
// template's details paragraph drops out.
type EventNotification struct {
	Action     string
	Details    string
	OccurredAt time.Time
	// AuditURL links to the organization's audit log, where the full record is.
	AuditURL string
}

// eventTimeLayout stamps the moment an event was recorded. It is UTC and
// numeric-only on purpose: a notification goes to an organization's admins, who
// may not share a locale, and this package's rule is that no copy lives in Go.
const eventTimeLayout = "2006-01-02 15:04 UTC"

// SendEventNotification tells an organization's admins about one event they
// subscribed to. Every admin is attempted even when one of them fails, and the
// failures come back joined: the dispatcher claims an event off the outbox before
// delivering it and never re-queues it, so one permanently rejecting mailbox would
// otherwise cost the notification for every admin after it (sendToEach).
//
// The mail renders in the deployment's locale, the same one for every admin of the
// org: neither a per-org nor a per-recipient language preference is stored yet.
// Returns ErrNotConfigured when the org has no usable SMTP settings, and an error
// when the catalogue has no name for the action — a notification that names the raw
// audit action would be worse than a logged gap.
func (s *Service) SendEventNotification(ctx context.Context, orgID uuid.UUID, recipients []string, orgName string, n EventNotification) error {
	locale := s.locale("")
	name, ok := EventLabel(n.Action, locale)
	if !ok {
		return fmt.Errorf("email: no %s name for event %q", locale, n.Action)
	}
	return s.sendToEach(ctx, orgID, KindEventNotification, locale, recipients, map[string]string{
		varOrgName:      orgName,
		varEventName:    name,
		varEventDetails: n.Details,
		varEventTime:    n.OccurredAt.UTC().Format(eventTimeLayout),
		varAuditURL:     n.AuditURL,
	})
}

// SendExportReady hands an organisation's data-portability bundle to its admins
// after the provider terminates service (Art 7(6)(f)). It uses sendToEach rather
// than sendLocalized because there is no retry behind it: the export ran once,
// and stopping at the first rejected admin would leave the rest unnotified with
// no second attempt coming.
func (s *Service) SendExportReady(ctx context.Context, orgID uuid.UUID, recipients []string, orgName, downloadURL, expiry string) error {
	return s.sendToEach(ctx, orgID, KindExportReady, s.locale(""), recipients, map[string]string{
		varOrgName:      orgName,
		varExportURL:    downloadURL,
		varExportExpiry: expiry,
	})
}

// SendSignedDocument delivers a completed co-signed document to its recipient as a
// PDF attachment, with the org's cover message. Returns ErrNotConfigured when the
// org has no usable SMTP settings.
func (s *Service) SendSignedDocument(ctx context.Context, orgID uuid.UUID, to, orgName, message, filename string, pdf []byte) error {
	cfg, msg, err := s.compose(ctx, orgID, KindSignedDocument, s.locale(""), map[string]string{
		varOrgName: orgName,
		varMessage: message,
	})
	if err != nil {
		return err
	}
	msg.To = to
	msg.Attachments = []mailer.Attachment{{Filename: filename, ContentType: "application/pdf", Bytes: pdf}}
	return s.sender.Send(cfg, msg)
}

// SendSignatureRequested tells a selected member that a document is waiting for
// their signature, linking to the signing page. Returns ErrNotConfigured when the
// org has no usable SMTP settings.
func (s *Service) SendSignatureRequested(ctx context.Context, orgID uuid.UUID, to, orgName, documentName, signingURL string) error {
	return s.sendLocalized(ctx, orgID, KindSignatureRequested, s.locale(""), []string{to}, map[string]string{
		varOrgName:      orgName,
		varDocumentName: documentName,
		varSigningURL:   signingURL,
	})
}

// SendSpecimen sends a sample of one kind to a single address, rendered from the
// org's own template (or the shipped default) with the kind's sample variables, so
// an admin can check a real, fully branded message of that cause against their own
// inbox rather than against a browser preview. KindSMTPTest is the SMTP self-test.
// An empty locale means the deployment default. Returns ErrNotConfigured when the
// org has no usable SMTP settings.
func (s *Service) SendSpecimen(ctx context.Context, orgID uuid.UUID, kind Kind, locale Locale, to, orgName string) error {
	resolved := s.locale(locale)
	vars, ok := SampleVariables(kind, resolved, orgName)
	if !ok {
		return fmt.Errorf("email: unknown mail kind %q", kind)
	}
	return s.sendLocalized(ctx, orgID, kind, resolved, []string{to}, vars)
}

// Preview renders one message without sending it, from the template the caller
// supplies (a tenant's unsaved draft) or the org's stored one when tpl is nil, so
// the editor's preview and a delivered message come out of the same renderer.
// Needs no SMTP configuration: an org previews its copy before it has a server.
func (s *Service) Preview(ctx context.Context, orgID uuid.UUID, kind Kind, locale Locale, tpl *Template, orgName string) (Body, error) {
	locale = s.locale(locale)
	vars, ok := SampleVariables(kind, locale, orgName)
	if !ok {
		return Body{}, fmt.Errorf("email: unknown mail kind %q", kind)
	}
	var resolved Template
	if tpl != nil {
		resolved = *tpl
	} else {
		stored, err := s.templateFor(ctx, orgID, kind, locale)
		if err != nil {
			return Body{}, err
		}
		resolved = stored
	}
	body, err := Render(kind, locale, resolved, s.brandWithLogo(ctx, orgID, resolved), vars)
	if err != nil {
		return Body{}, &InvalidTemplateError{Reason: err}
	}
	// The preview iframe cannot resolve cid:, so the logo rides inline as a data:
	// URI instead — the same image, shown the way a sandboxed frame can render it.
	return inlinePreviewLogo(body), nil
}

// send resolves the org's SMTP config, locale, template and branding, renders the
// message once and delivers it to every recipient.
func (s *Service) send(ctx context.Context, orgID uuid.UUID, kind Kind, recipients []string, vars map[string]string) error {
	return s.sendLocalized(ctx, orgID, kind, s.locale(""), recipients, vars)
}

// locale resolves an explicit preference against the deployment default. Empty
// means "no preference". Per-recipient and per-org language preferences are not
// stored yet, so the deployment default is the only other preference there is;
// ResolveLocale already takes them in recipient -> org -> en order for when they
// are (see .ai/features/email-templates.md).
func (s *Service) locale(preference Locale) Locale {
	return ResolveLocale(string(preference), string(s.defaultLocale))
}

// sendLocalized renders the message once and delivers it to every recipient,
// stopping at the first failure: the flows that use it act on that error (a
// credential offer, a PostGuard package) and the send is retried by retrying the
// flow.
func (s *Service) sendLocalized(ctx context.Context, orgID uuid.UUID, kind Kind, locale Locale, recipients []string, vars map[string]string) error {
	cfg, msg, err := s.compose(ctx, orgID, kind, locale, vars)
	if err != nil {
		return err
	}
	for _, to := range recipients {
		msg.To = to
		if err := s.sender.Send(cfg, msg); err != nil {
			return err
		}
	}
	return nil
}

// sendToEach delivers to every recipient even when one of them fails, and returns
// the failures joined, each naming the address it belongs to. It is for a message
// with no second chance: there one rejecting mailbox has to cost its own recipient
// and not the rest of the list, which is the opposite trade-off from sendLocalized.
func (s *Service) sendToEach(ctx context.Context, orgID uuid.UUID, kind Kind, locale Locale, recipients []string, vars map[string]string) error {
	cfg, msg, err := s.compose(ctx, orgID, kind, locale, vars)
	if err != nil {
		return err
	}
	var failures []error
	for _, to := range recipients {
		msg.To = to
		if err := s.sender.Send(cfg, msg); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", to, err))
		}
	}
	return errors.Join(failures...)
}

// compose resolves the org's SMTP config, template and branding and renders the
// message, ready to hand to the sender once per recipient. The returned message
// carries no To: the caller sets it per recipient, so the body is rendered once
// however many addresses it goes to.
func (s *Service) compose(ctx context.Context, orgID uuid.UUID, kind Kind, locale Locale, vars map[string]string) (mailer.Config, mailer.Message, error) {
	resolved, ok, err := s.settings.configFor(ctx, orgID)
	if err != nil {
		return mailer.Config{}, mailer.Message{}, err
	}
	if !ok {
		return mailer.Config{}, mailer.Message{}, ErrNotConfigured
	}
	cfg, err := s.authenticate(ctx, resolved)
	if err != nil {
		return mailer.Config{}, mailer.Message{}, err
	}

	tpl, err := s.templateFor(ctx, orgID, kind, locale)
	if err != nil {
		return mailer.Config{}, mailer.Message{}, err
	}

	body, err := Render(kind, locale, tpl, s.brandWithLogo(ctx, orgID, tpl), vars)
	if err != nil {
		return mailer.Config{}, mailer.Message{}, err
	}

	var inline []mailer.InlineImage
	if body.InlineLogo != nil {
		inline = []mailer.InlineImage{{
			ContentID:   body.InlineLogo.ContentID,
			ContentType: body.InlineLogo.ContentType,
			Bytes:       body.InlineLogo.Bytes,
		}}
	}
	return cfg, mailer.Message{
		Subject:  body.Subject,
		TextBody: body.TextBody,
		HTMLBody: body.HTMLBody,
		Inline:   inline,
	}, nil
}

// authenticate completes the wire config with the credential the send presents.
// For a password org that is already done; for an XOAUTH2 org it mints the
// bearer token, which is short-lived and so belongs to the send rather than to
// the settings row.
func (s *Service) authenticate(ctx context.Context, resolved sendConfig) (mailer.Config, error) {
	cfg := resolved.Mailer
	if cfg.AuthMechanism != mailer.AuthXOAuth2 {
		return cfg, nil
	}
	if s.tokens == nil {
		return mailer.Config{}, fmt.Errorf("email: %s is configured but this deployment has no token source", mailer.AuthXOAuth2)
	}
	token, err := s.tokens.Token(ctx, resolved.OAuth)
	if err != nil {
		return mailer.Config{}, fmt.Errorf("email: acquiring the smtp access token: %w", err)
	}
	cfg.AccessToken = token
	return cfg, nil
}

// templateFor resolves the template to render: the org's own edit when it has
// one, the shipped default otherwise. A lookup failure logs and falls back to the
// shipped copy for the same reason branding does — a message in the default
// wording still delivers, a message not sent breaks the flow it belongs to.
func (s *Service) templateFor(ctx context.Context, orgID uuid.UUID, kind Kind, locale Locale) (Template, error) {
	if s.templates != nil {
		tpl, err := s.templates.ResolveTemplate(ctx, orgID, kind, locale)
		if err == nil {
			return tpl, nil
		}
		slog.WarnContext(ctx, "resolving the organization's mail template failed, sending the shipped default",
			"org_id", orgID, "kind", kind, "locale", locale, "error", err)
	}
	tpl, ok := DefaultTemplate(kind, locale)
	if !ok {
		return Template{}, fmt.Errorf("email: no template for kind %q", kind)
	}
	return tpl, nil
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

// brandWithLogo resolves the palette and, only when the layout actually has a logo
// block, the org's logo image — so a template without one never triggers a logo
// read. A logo fetch failure is cosmetic (the block falls back to the wordmark) and
// never blocks the send.
func (s *Service) brandWithLogo(ctx context.Context, orgID uuid.UUID, tpl Template) Brand {
	brand := s.brandFor(ctx, orgID)
	if templateHasLogoBlock(tpl) {
		brand.Logo = s.logoFor(ctx, orgID)
	}
	return brand
}

// logoFor resolves the org's logo image, returning an empty Logo (the wordmark
// fallback) when there is no brand source, none is set, or the lookup fails.
func (s *Service) logoFor(ctx context.Context, orgID uuid.UUID) Logo {
	if s.brand == nil {
		return Logo{}
	}
	logo, err := s.brand.MailLogo(ctx, orgID)
	if err != nil {
		slog.WarnContext(ctx, "resolving the mail logo failed, sending without the logo image",
			"org_id", orgID, "error", err)
		return Logo{}
	}
	return logo
}
