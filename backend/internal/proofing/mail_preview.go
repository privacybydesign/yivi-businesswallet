package proofing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/themesettings"
)

// ErrNoMailPreview is PreviewMail's answer on a deployment whose mailer
// cannot render one.
var ErrNoMailPreview = errors.New("proofing: the mailer renders no preview")

type mailPreviewer interface {
	PreviewIdentityProofingRequested(ctx context.Context, orgID uuid.UUID, m email.ProofingMail, theme *email.PreviewTheme) (email.Body, error)
}

// MailPreviewInput is an unsaved branding form: the org's (its name, support
// contact and privacy link), or with FlowID a flow's own (Custom false is the
// org's), with what the form does to the flow's logo, and Theme the unsaved
// wallet theme (nil is the saved one).
type MailPreviewInput struct {
	FlowID   string
	Custom   bool
	Branding Branding
	Logo     LogoChange
	Theme    *email.PreviewTheme
	Locale   email.Locale
}

// PreviewMail renders the proofing mail an external person would get with
// in's branding over what is saved, as proofingMail styles a real one.
func (s *Service) PreviewMail(ctx context.Context, org Org, in MailPreviewInput) (email.Body, error) {
	previewer, ok := s.mailer.(mailPreviewer)
	if !ok {
		return email.Body{}, ErrNoMailPreview
	}
	draft, err := checkBranding(in.Branding)
	if err != nil {
		return email.Body{}, err
	}
	settings, err := s.orgSettings.Get(ctx, org.ID)
	if err != nil {
		return email.Body{}, err
	}
	if settings.OrgName == "" {
		settings.OrgName = org.Name
	}
	var logo Logo
	if in.FlowID == "" {
		settings.Branding.DisplayName = draft.DisplayName
		settings.Branding.SupportContact = draft.SupportContact
		settings.Branding.PrivacyURL = draft.PrivacyURL
	} else {
		settings, logo, err = s.previewFlow(ctx, org, settings, draft, in)
		if err != nil {
			return email.Body{}, err
		}
	}
	b := settings.Branding
	body, err := previewer.PreviewIdentityProofingRequested(ctx, org.ID, email.ProofingMail{
		OrgName: settings.SignedAs(), RequesterName: settings.SignedAs(),
		SupportContact: b.SupportContact, PrivacyURL: b.PrivacyURL, Locale: in.Locale,
		Brand: &email.ProofingBrand{PrimaryColor: b.PrimaryColor, Logo: email.Logo{Bytes: logo.Bytes, ContentType: logo.ContentType}},
	}, in.Theme)
	if err != nil {
		return email.Body{}, fmt.Errorf("proofing: mail preview: %w", err)
	}
	return body, nil
}

// previewFlow is the flow form's branding over the org's, and the flow's logo:
// a picked file, else its saved one unless removed, else none (the theme's
// shows).
func (s *Service) previewFlow(ctx context.Context, org Org, settings Settings, draft Branding, in MailPreviewInput) (Settings, Logo, error) {
	if err := s.orgHasFlow(ctx, org, in.FlowID); err != nil {
		return Settings{}, Logo{}, err
	}
	saved, err := s.flowBranding(ctx, org.ID, in.FlowID)
	if err != nil {
		return Settings{}, Logo{}, err
	}
	settings = settings.brandedFor(FlowBranding{Custom: in.Custom, Branding: draft})
	switch {
	case !in.Custom:
		return settings, Logo{}, nil
	case in.Logo.Replace:
		return settings, in.Logo.Logo, nil
	}
	logo, err := s.brandLogo(ctx, org.ID, in.FlowID, saved)
	if errors.Is(err, ErrNoLogo) {
		return settings, Logo{}, nil
	}
	return settings, logo, err
}

type mailPreviewResponse struct {
	Subject string `json:"subject"`
	HTML    string `json:"html"`
}

// mailPreviewFormBytes allows a flow logo and a theme logo in one form.
const mailPreviewFormBytes = 2*themesettings.MaxLogoBytes + brandingBodySlack

// previewBrandingMail takes a branding form (displayName, supportContact,
// privacyUrl; with flowId a flow's, plus custom, primaryColor and a logo or
// removeLogo), the unsaved theme when theme=true (themePrimaryColor,
// themeTextColor, themeSurfaceColor, themeBorderColor, themeLinkColor,
// themeFontFamily, and a themeLogo file, themeRemoveLogo=true, or neither to
// keep the saved logo) and locale, and answers the proofing mail it gives.
func (h *Handler) previewBrandingMail(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, mailPreviewFormBytes)
	if err := r.ParseMultipartForm(brandingFormMemory); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return &respond.APIError{Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large", Message: "the logo is too large"}
		}
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "invalid multipart form"}
	}
	logo, err := parseLogo(r)
	if err != nil {
		return err
	}
	theme, err := parsePreviewTheme(r)
	if err != nil {
		return err
	}
	// An unknown locale is the deployment default.
	locale, _ := email.ParseLocale(r.FormValue("locale"))
	body, err := h.service.PreviewMail(r.Context(), orgFromRequest(r), MailPreviewInput{
		FlowID: r.FormValue("flowId"),
		Custom: r.FormValue("custom") == "true",
		Branding: Branding{
			DisplayName:    r.FormValue("displayName"),
			PrimaryColor:   r.FormValue("primaryColor"),
			SupportContact: r.FormValue("supportContact"),
			PrivacyURL:     r.FormValue("privacyUrl"),
		},
		Logo:   logo,
		Theme:  theme,
		Locale: locale,
	})
	if errors.Is(err, ErrNoMailPreview) {
		return &respond.APIError{Status: http.StatusNotImplemented, Code: "no_mail_preview", Message: "this deployment renders no mail preview"}
	}
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, mailPreviewResponse{Subject: body.Subject, HTML: body.HTMLBody})
	return nil
}

// parsePreviewTheme reads the unsaved theme of a mail preview form, nil
// without theme=true.
func parsePreviewTheme(r *http.Request) (*email.PreviewTheme, error) {
	if r.FormValue("theme") != "true" {
		return nil, nil
	}
	seeds := email.Seeds{
		PrimaryColor: strings.TrimSpace(r.FormValue("themePrimaryColor")),
		TextColor:    strings.TrimSpace(r.FormValue("themeTextColor")),
		SurfaceColor: strings.TrimSpace(r.FormValue("themeSurfaceColor")),
		BorderColor:  strings.TrimSpace(r.FormValue("themeBorderColor")),
		LinkColor:    strings.TrimSpace(r.FormValue("themeLinkColor")),
		FontFamily:   strings.TrimSpace(r.FormValue("themeFontFamily")),
	}
	for _, c := range []string{seeds.PrimaryColor, seeds.TextColor, seeds.SurfaceColor, seeds.BorderColor, seeds.LinkColor} {
		if c != "" && !hexColor.MatchString(c) {
			return nil, &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_input", Message: "a theme colour must be a hex colour like #1f5b4a"}
		}
	}
	if seeds.FontFamily != "" && !themesettings.ValidFontFamily(seeds.FontFamily) {
		return nil, &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_input", Message: "the theme font must be a comma-separated font-family list"}
	}
	theme := &email.PreviewTheme{Seeds: seeds}
	file, _, err := r.FormFile("themeLogo")
	switch {
	case errors.Is(err, http.ErrMissingFile):
		theme.KeepLogo = r.FormValue("themeRemoveLogo") != "true"
		return theme, nil
	case err != nil:
		return nil, &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "invalid theme logo upload"}
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, themesettings.MaxLogoBytes+1))
	if err != nil {
		return nil, fmt.Errorf("proofing: read previewed theme logo: %w", err)
	}
	contentType, ok := themesettings.DetectLogoType(data)
	if !ok || len(data) > themesettings.MaxLogoBytes {
		return nil, &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_input", Message: "the theme logo must be a PNG, JPEG, GIF, WebP or SVG image of at most 512 KiB"}
	}
	theme.Logo = email.Logo{Bytes: data, ContentType: contentType}
	return theme, nil
}
