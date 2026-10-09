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

// brandingResponse is a customer's branding as the app shows it; LogoURI is
// "" without a logo and carries a version so a replaced logo is re-fetched.
type brandingResponse struct {
	DisplayName    string `json:"displayName"`
	PrimaryColor   string `json:"primaryColor"`
	SupportContact string `json:"supportContact"`
	PrivacyURL     string `json:"privacyUrl"`
	LogoURI        string `json:"logoUri"`
	// HidePoweredBy leaves the "powered by" line off the hosted page.
	HidePoweredBy bool `json:"hidePoweredBy"`
}

func newBrandingResponse(slug string, c Customer) brandingResponse {
	b := brandingResponse{
		DisplayName: c.Branding.DisplayName, PrimaryColor: c.Branding.PrimaryColor,
		SupportContact: c.Branding.SupportContact, PrivacyURL: c.Branding.PrivacyURL,
		HidePoweredBy: c.Branding.HidePoweredBy,
	}
	if c.Branding.HasLogo {
		b.LogoURI = fmt.Sprintf("/api/v1/orgs/%s/customers/%s/logo?v=%d",
			url.PathEscape(slug), c.ID, c.UpdatedAt.Unix())
	}
	return b
}

// saveBranding takes a multipart form, like the org theme: the text fields,
// and a "logo" file to replace the logo or "removeLogo=true" to clear it;
// neither keeps it.
func (h *Handler) saveBranding(w http.ResponseWriter, r *http.Request) error {
	id, err := customerIDFromPath(r)
	if err != nil {
		return err
	}
	r.Body = http.MaxBytesReader(w, r.Body, themesettings.MaxLogoBytes+brandingBodySlack)
	if err := r.ParseMultipartForm(brandingFormMemory); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return &respond.APIError{Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large", Message: "the logo is too large"}
		}
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "invalid multipart form"}
	}
	logo, err := parseCustomerLogo(r)
	if err != nil {
		return err
	}
	c, err := h.service.SaveCustomerBranding(r.Context(), orgFromRequest(r).ID, id, CustomerBranding{
		DisplayName:    r.FormValue("displayName"),
		PrimaryColor:   r.FormValue("primaryColor"),
		SupportContact: r.FormValue("supportContact"),
		PrivacyURL:     r.FormValue("privacyUrl"),
		HidePoweredBy:  r.FormValue("hidePoweredBy") == "true",
	}, logo)
	if err != nil {
		return mapError(err)
	}
	return h.respondCustomer(w, r, c)
}

func parseCustomerLogo(r *http.Request) (LogoChange, error) {
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
	return LogoChange{Replace: true, Logo: CustomerLogo{Bytes: data, ContentType: contentType}}, nil
}

func (h *Handler) serveCustomerLogo(w http.ResponseWriter, r *http.Request) error {
	id, err := customerIDFromPath(r)
	if err != nil {
		return err
	}
	logo, err := h.service.CustomerLogo(r.Context(), orgFromRequest(r).ID, id)
	if err != nil {
		return mapError(err)
	}
	themesettings.SetLogoResponseHeaders(w.Header(), logo.ContentType)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(logo.Bytes); err != nil {
		// The status is committed; a failed write can only be logged.
		slog.ErrorContext(r.Context(), "proofing: write customer logo", slog.Any("error", err))
	}
	return nil
}
