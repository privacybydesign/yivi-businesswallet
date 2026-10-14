package proofingengine

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/themesettings"
)

// Branding is how the Idem app dresses a session, so it looks like the
// requester's own app: its name, its theme's #rrggbb colours and font ("" keeps
// the app's own), and whether a logo is served at GET /app/{token}/branding/logo.
type Branding struct {
	Name string `json:"name"`
	// PrimaryColor and SecondaryColor carry the brand: buttons, links and
	// progress, and labels and badges.
	PrimaryColor   string `json:"primaryColor,omitempty"`
	SecondaryColor string `json:"secondaryColor,omitempty"`
	TextColor      string `json:"textColor,omitempty"`
	SurfaceColor   string `json:"surfaceColor,omitempty"`
	BorderColor    string `json:"borderColor,omitempty"`
	LinkColor      string `json:"linkColor,omitempty"`
	SuccessColor   string `json:"successColor,omitempty"`
	WarningColor   string `json:"warningColor,omitempty"`
	ErrorColor     string `json:"errorColor,omitempty"`
	// HeaderColor is the dark top of the app's screens.
	HeaderColor string `json:"headerColor,omitempty"`
	// FontFamily is a CSS font-family list, as the org theme stores it.
	FontFamily string `json:"fontFamily,omitempty"`
	HasLogo    bool   `json:"hasLogo"`
}

// BrandingLogo is a session's logo image.
type BrandingLogo struct {
	Bytes       []byte
	ContentType string
}

// ErrNoBrandingLogo is BrandingLookup.Logo's answer for a session without one.
var ErrNoBrandingLogo = errors.New("proofingengine: session has no branding logo")

// BrandingLookup resolves a session's branding from the session itself: the
// tenant (org) and client reference (the wallet's request) the session was
// created with. Nothing the app sends picks it, so a session only ever shows
// the branding of the org, or the org's customer, that requested it.
type BrandingLookup interface {
	// Branding is nil when the session gets the app's own look.
	Branding(ctx context.Context, tenantID, clientReference string) (*Branding, error)
	Logo(ctx context.Context, tenantID, clientReference string) (BrandingLogo, error)
}

// sessionBranding is the session's branding, or nil for the app's own look; a
// failed lookup is logged and also gives the app's own look, rather than
// failing the session read.
func (s *Server) sessionBranding(ctx context.Context, tenantID, clientReference string) *Branding {
	if s.cfg.Branding == nil {
		return nil
	}
	branding, err := s.cfg.Branding.Branding(ctx, tenantID, clientReference)
	if err != nil {
		slog.ErrorContext(ctx, "proofing: resolve session branding", slog.Any("error", err))
		return nil
	}
	return branding
}

// handleBrandingLogo serves the session's logo to a device holding the
// session, with the same locked-down headers as every other stored logo.
func (s *Server) handleBrandingLogo(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := s.appSessionByPathToken(w, r)
	if !ok {
		return
	}
	if s.cfg.Branding == nil {
		writeError(w, http.StatusNotFound, "no branding logo")
		return
	}
	logo, err := s.cfg.Branding.Logo(r.Context(), sess.TenantID, sess.ClientReference)
	if errors.Is(err, ErrNoBrandingLogo) {
		writeError(w, http.StatusNotFound, "no branding logo")
		return
	}
	if err != nil {
		writeInternalError(w, r, "could not read branding logo", err)
		return
	}
	themesettings.SetLogoResponseHeaders(w.Header(), logo.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(logo.Bytes)))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(logo.Bytes); err != nil {
		slog.ErrorContext(r.Context(), "proofing: write branding logo", slog.Any("error", err))
	}
}
