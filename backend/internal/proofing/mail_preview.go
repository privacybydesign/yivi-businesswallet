package proofing

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// ErrNoMailPreview is PreviewMail's answer on a deployment whose mailer
// cannot render one.
var ErrNoMailPreview = errors.New("proofing: the mailer renders no preview")

type mailPreviewer interface {
	PreviewIdentityProofingRequested(ctx context.Context, orgID uuid.UUID, m email.ProofingMail) (email.Body, error)
}

// MailPreviewInput is an unsaved branding form: the org's branding, or with
// FlowID the flow's own (Custom false is the org's saved branding), with what
// the form does to the logo.
type MailPreviewInput struct {
	FlowID   string
	Custom   bool
	Branding Branding
	Logo     LogoChange
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
		logo, err = s.previewOrgLogo(ctx, org, settings, in.Logo)
		draft.HidePoweredBy = settings.Branding.HidePoweredBy
		settings.Branding = draft
	} else {
		logo, settings, err = s.previewFlowLogo(ctx, org, settings, draft, in)
	}
	if err != nil {
		return email.Body{}, err
	}
	b := settings.Branding
	body, err := previewer.PreviewIdentityProofingRequested(ctx, org.ID, email.ProofingMail{
		OrgName: settings.SignedAs(), RequesterName: settings.SignedAs(),
		SupportContact: b.SupportContact, PrivacyURL: b.PrivacyURL, Locale: in.Locale,
		Brand: &email.ProofingBrand{PrimaryColor: b.PrimaryColor, Logo: email.Logo{Bytes: logo.Bytes, ContentType: logo.ContentType}},
	})
	if err != nil {
		return email.Body{}, fmt.Errorf("proofing: mail preview: %w", err)
	}
	return body, nil
}

// previewOrgLogo is the org form's logo: a picked file, none once removed,
// else the saved one.
func (s *Service) previewOrgLogo(ctx context.Context, org Org, settings Settings, change LogoChange) (Logo, error) {
	if change.Replace {
		return change.Logo, nil
	}
	if !settings.Branding.HasLogo {
		return Logo{}, nil
	}
	return s.readPreviewLogo(s.orgSettings.Logo(ctx, org.ID))
}

// previewFlowLogo is the flow form's settings, its branding over the org's,
// and its logo: a picked file, the flow's saved one unless removed, else the
// org's.
func (s *Service) previewFlowLogo(ctx context.Context, org Org, settings Settings, draft Branding, in MailPreviewInput) (Logo, Settings, error) {
	if err := s.orgHasFlow(ctx, org, in.FlowID); err != nil {
		return Logo{}, Settings{}, err
	}
	saved, err := s.flowBranding(ctx, org.ID, in.FlowID)
	if err != nil {
		return Logo{}, Settings{}, err
	}
	own := FlowBranding{Custom: in.Custom, Branding: draft}
	own.HasLogo = in.Custom && (in.Logo.Replace && len(in.Logo.Logo.Bytes) > 0 || !in.Logo.Replace && saved.Custom && saved.HasLogo)
	settings = settings.brandedFor(own)
	switch {
	case !settings.Branding.HasLogo:
		return Logo{}, settings, nil
	case own.HasLogo && in.Logo.Replace:
		return in.Logo.Logo, settings, nil
	}
	logo, err := s.readPreviewLogo(flowOrOrgLogo(ctx, s.flowBrandings, s.orgSettings, org.ID, in.FlowID, own))
	return logo, settings, err
}

// readPreviewLogo is a saved logo, or none when it is gone: the preview still
// shows the rest.
func (s *Service) readPreviewLogo(logo Logo, err error) (Logo, error) {
	if errors.Is(err, ErrNoLogo) {
		return Logo{}, nil
	}
	return logo, err
}

type mailPreviewResponse struct {
	Subject string `json:"subject"`
	HTML    string `json:"html"`
}

// previewBrandingMail takes the branding form as saveBranding and
// saveFlowBranding do, plus "flowId" for a flow's and "locale", and answers
// the proofing mail it gives.
func (h *Handler) previewBrandingMail(w http.ResponseWriter, r *http.Request) error {
	logo, err := readBrandingForm(w, r)
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
