package themesettings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// colorPattern constrains a theme colour to a 6-digit CSS hex string (e.g.
// "#1d4e89"). The frontend derives tints/shades and a readable foreground from
// it, so the format is fixed rather than accepting arbitrary CSS colour syntax.
var colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// MaxLogoURILength caps a logo URI so a data: URI cannot bloat the theme
// response (which every member fetches to render the wallet). ~256 KiB of URI
// comfortably holds a small optimised PNG/SVG data URI.
const MaxLogoURILength = 256 * 1024

type settingsStore interface {
	GetSettings(ctx context.Context, orgID uuid.UUID) (Settings, error)
	Upsert(ctx context.Context, orgID uuid.UUID, in SettingsInput) (Settings, error)
}

// Handler serves org-scoped theme settings. Reads are open to any member so the
// app themes itself for everyone; writes are org-admin only.
type Handler struct {
	store       settingsStore
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(store settingsStore, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{store: store, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	member := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(next))
	}
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}
	mux.Handle("GET /orgs/{slug}/theme", member(respond.HandlerFunc(h.getSettings)))
	mux.Handle("PUT /orgs/{slug}/theme", admin(respond.HandlerFunc(h.putSettings)))
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	settings, err := h.store.GetSettings(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("getting theme settings: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, settings)
	return nil
}

type settingsRequest struct {
	PrimaryColor string `json:"primaryColor"`
	AccentColor  string `json:"accentColor"`
	LogoURI      string `json:"logoUri"`
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) error {
	var req settingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}

	in := SettingsInput{
		PrimaryColor: strings.TrimSpace(req.PrimaryColor),
		AccentColor:  strings.TrimSpace(req.AccentColor),
		LogoURI:      strings.TrimSpace(req.LogoURI),
	}
	if err := validate(in); err != nil {
		return err
	}

	org := organization.OrgFromContext(r.Context())
	settings, err := h.store.Upsert(r.Context(), org.ID, in)
	if err != nil {
		return fmt.Errorf("updating theme settings: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, settings)
	return nil
}

// validate enforces the field formats. Empty strings are allowed everywhere —
// they clear a field back to the default look.
func validate(in SettingsInput) error {
	if in.PrimaryColor != "" && !colorPattern.MatchString(in.PrimaryColor) {
		return badRequest("invalid_input", "primaryColor must be a hex colour like #1d4e89")
	}
	if in.AccentColor != "" && !colorPattern.MatchString(in.AccentColor) {
		return badRequest("invalid_input", "accentColor must be a hex colour like #1d4e89")
	}
	if len(in.LogoURI) > MaxLogoURILength {
		return badRequest("invalid_input", "logoUri is too large")
	}
	if in.LogoURI != "" && !isSafeLogoURI(in.LogoURI) {
		return badRequest("invalid_input", "logoUri must be an https, http or data:image URI")
	}
	return nil
}

// isSafeLogoURI keeps the logo to schemes that render as an image and cannot
// carry script (no javascript:, no data:text/html).
func isSafeLogoURI(uri string) bool {
	switch {
	case strings.HasPrefix(uri, "https://"), strings.HasPrefix(uri, "http://"):
		return true
	case strings.HasPrefix(uri, "data:image/"):
		return true
	default:
		return false
	}
}

func badRequest(code, msg string) error {
	return &respond.APIError{Status: http.StatusBadRequest, Code: code, Message: msg}
}
