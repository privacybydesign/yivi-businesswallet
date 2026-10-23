package proofing

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/themesettings"
)

const (
	// brandingFormMemory is how much of the branding form is buffered in RAM.
	brandingFormMemory = 1 << 20
	// brandingBodySlack allows for the boundaries and text fields on top of
	// the logo cap.
	brandingBodySlack = 1 << 20
	logoFormField     = "logo"
)

// brandingResponse is the org's branding as the app shows it; LogoURI is
// "" without a logo and carries a version so a replaced logo is re-fetched.
type brandingResponse struct {
	DisplayName    string `json:"displayName"`
	PrimaryColor   string `json:"primaryColor"`
	SecondaryColor string `json:"secondaryColor"`
	TextColor      string `json:"textColor"`
	SurfaceColor   string `json:"surfaceColor"`
	BorderColor    string `json:"borderColor"`
	LinkColor      string `json:"linkColor"`
	SuccessColor   string `json:"successColor"`
	WarningColor   string `json:"warningColor"`
	ErrorColor     string `json:"errorColor"`
	TopbarColor    string `json:"topbarColor"`
	FontFamily     string `json:"fontFamily"`
	SupportContact string `json:"supportContact"`
	PrivacyURL     string `json:"privacyUrl"`
	LogoURI        string `json:"logoUri"`
	// HidePoweredBy leaves the "powered by" line off the hosted page.
	HidePoweredBy bool `json:"hidePoweredBy"`
}

func newBrandingResponse(slug string, s Settings) brandingResponse {
	b := brandingResponse{
		DisplayName: s.Branding.DisplayName, PrimaryColor: s.Branding.PrimaryColor,
		SecondaryColor: s.Branding.SecondaryColor, TextColor: s.Branding.TextColor,
		SurfaceColor: s.Branding.SurfaceColor, BorderColor: s.Branding.BorderColor, LinkColor: s.Branding.LinkColor,
		SuccessColor: s.Branding.SuccessColor, WarningColor: s.Branding.WarningColor, ErrorColor: s.Branding.ErrorColor,
		TopbarColor: s.Branding.TopbarColor, FontFamily: s.Branding.FontFamily,
		SupportContact: s.Branding.SupportContact, PrivacyURL: s.Branding.PrivacyURL,
		HidePoweredBy: s.Branding.HidePoweredBy,
	}
	if s.Branding.HasLogo {
		b.LogoURI = fmt.Sprintf("/api/v1/orgs/%s/identity-proofing/settings/logo?v=%d",
			url.PathEscape(slug), s.UpdatedAt.Unix())
	}
	return b
}

// saveBranding takes a multipart form, like the org theme: the text fields,
// and a "logo" file to replace the logo or "removeLogo=true" to clear it;
// neither keeps it.
func (h *Handler) saveBranding(w http.ResponseWriter, r *http.Request) error {
	logo, err := readBrandingForm(w, r)
	if err != nil {
		return err
	}
	settings, err := h.service.SaveBranding(r.Context(), orgFromRequest(r).ID, Branding{
		DisplayName:    r.FormValue("displayName"),
		PrimaryColor:   r.FormValue("primaryColor"),
		SecondaryColor: r.FormValue("secondaryColor"),
		TextColor:      r.FormValue("textColor"),
		SurfaceColor:   r.FormValue("surfaceColor"),
		BorderColor:    r.FormValue("borderColor"),
		LinkColor:      r.FormValue("linkColor"),
		SuccessColor:   r.FormValue("successColor"),
		WarningColor:   r.FormValue("warningColor"),
		ErrorColor:     r.FormValue("errorColor"),
		TopbarColor:    r.FormValue("topbarColor"),
		FontFamily:     r.FormValue("fontFamily"),
		SupportContact: r.FormValue("supportContact"),
		PrivacyURL:     r.FormValue("privacyUrl"),
		HidePoweredBy:  r.FormValue("hidePoweredBy") == "true",
	}, logo)
	if err != nil {
		return mapError(err)
	}
	return h.respondSettings(w, r, settings)
}

// readBrandingForm parses a branding form and returns what it does to the logo.
func readBrandingForm(w http.ResponseWriter, r *http.Request) (LogoChange, error) {
	if err := parseBrandingForm(w, r); err != nil {
		return LogoChange{}, err
	}
	return parseLogo(r)
}

func parseBrandingForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, themesettings.MaxLogoBytes+brandingBodySlack)
	if err := r.ParseMultipartForm(brandingFormMemory); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return &respond.APIError{Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large", Message: "the logo is too large"}
		}
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "invalid multipart form"}
	}
	return nil
}

func parseLogo(r *http.Request) (LogoChange, error) {
	file, _, err := r.FormFile(logoFormField)
	if errors.Is(err, http.ErrMissingFile) {
		return LogoChange{Replace: r.FormValue("removeLogo") == strconv.FormatBool(true)}, nil
	}
	if err != nil {
		return LogoChange{}, &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "invalid logo upload"}
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, themesettings.MaxLogoBytes+1))
	if err != nil {
		return LogoChange{}, fmt.Errorf("proofing: read uploaded logo: %w", err)
	}
	switch {
	case len(data) == 0:
		return LogoChange{}, &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_input", Message: "the logo file is empty"}
	case len(data) > themesettings.MaxLogoBytes:
		return LogoChange{}, &respond.APIError{Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large", Message: "the logo is too large"}
	}
	contentType, ok := themesettings.DetectLogoType(data)
	if !ok || strings.HasPrefix(contentType, "image/svg") {
		// Mail clients do not render SVG, and this logo is for mail.
		return LogoChange{}, &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_input", Message: "the logo must be a PNG, JPEG, GIF or WebP image"}
	}
	return LogoChange{Replace: true, Logo: Logo{Bytes: data, ContentType: contentType}}, nil
}

func (h *Handler) serveLogo(w http.ResponseWriter, r *http.Request) error {
	logo, err := h.service.Logo(r.Context(), orgFromRequest(r).ID)
	if err != nil {
		return mapError(err)
	}
	writeLogo(w, r, logo)
	return nil
}

func writeLogo(w http.ResponseWriter, r *http.Request, logo Logo) {
	themesettings.SetLogoResponseHeaders(w.Header(), logo.ContentType)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(logo.Bytes); err != nil {
		// The status is committed; a failed write can only be logged.
		slog.ErrorContext(r.Context(), "proofing: write logo", slog.Any("error", err))
	}
}
