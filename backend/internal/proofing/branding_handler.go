package proofing

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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

// brandingResponse is a branding as the app shows it. An org's has no colour
// or logo of its own (its theme is its look); a hosted page's carries its
// flow's. LogoURI is "" without a logo and carries a version so a replaced
// logo is re-fetched.
type brandingResponse struct {
	DisplayName    string `json:"displayName"`
	PrimaryColor   string `json:"primaryColor"`
	SupportContact string `json:"supportContact"`
	PrivacyURL     string `json:"privacyUrl"`
	LogoURI        string `json:"logoUri"`
	// HidePoweredBy leaves the "powered by" line off the hosted page.
	HidePoweredBy bool `json:"hidePoweredBy"`
	// ShowTheme puts the org's theme on the Idem app and the hosted page.
	ShowTheme bool `json:"showTheme"`
}

func newBrandingResponse(s Settings) brandingResponse {
	return brandingResponse{
		DisplayName: s.Branding.DisplayName, SupportContact: s.Branding.SupportContact,
		PrivacyURL: s.Branding.PrivacyURL, HidePoweredBy: s.Branding.HidePoweredBy, ShowTheme: s.Branding.ShowTheme,
	}
}

// saveBranding takes a multipart form: displayName, supportContact,
// privacyUrl, hidePoweredBy and showTheme; a form without showTheme keeps it
// as it is.
func (h *Handler) saveBranding(w http.ResponseWriter, r *http.Request) error {
	if err := parseBrandingForm(w, r); err != nil {
		return err
	}
	orgID := orgFromRequest(r).ID
	showTheme := r.FormValue("showTheme") == "true"
	if !r.Form.Has("showTheme") {
		current, err := h.service.Settings(r.Context(), orgID)
		if err != nil {
			return mapError(err)
		}
		showTheme = current.Branding.ShowTheme
	}
	settings, err := h.service.SaveBranding(r.Context(), orgID, Branding{
		DisplayName:    r.FormValue("displayName"),
		SupportContact: r.FormValue("supportContact"),
		PrivacyURL:     r.FormValue("privacyUrl"),
		HidePoweredBy:  r.FormValue("hidePoweredBy") == "true",
		ShowTheme:      showTheme,
	})
	if err != nil {
		return mapError(err)
	}
	return h.respondSettings(w, r, settings)
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

func writeLogo(w http.ResponseWriter, r *http.Request, logo Logo) {
	themesettings.SetLogoResponseHeaders(w.Header(), logo.ContentType)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(logo.Bytes); err != nil {
		// The status is committed; a failed write can only be logged.
		slog.ErrorContext(r.Context(), "proofing: write logo", slog.Any("error", err))
	}
}
