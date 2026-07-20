package issuersettings

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// instanceNamePattern constrains an issuer instance name to a safe URL path
// segment (it becomes the {instance} segment of the hosted issuer URL and a
// filename in the ops config repo).
var instanceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

const (
	// MaxLogoBytes caps an uploaded logo. The issuer logo is embedded as a data:
	// URI in the generated metadata a wallet shows, so an optimised PNG/SVG of a
	// few KiB is expected; 512 KiB leaves ample room without bloating the bundle.
	MaxLogoBytes = 512 << 10
	// multipartMemory is how much of the form is buffered in RAM before spilling
	// to temp files during parsing.
	multipartMemory = 1 << 20
	// bodySlack allows for multipart boundaries and the text fields on top of the
	// logo payload cap.
	bodySlack = 1 << 20
	// logoFormField is the multipart file field carrying the logo.
	logoFormField = "logo"
)

type settingsStore interface {
	GetSettings(ctx context.Context, orgID uuid.UUID) (Settings, error)
	GetLogo(ctx context.Context, orgID uuid.UUID) (Logo, error)
	Upsert(ctx context.Context, orgID uuid.UUID, in SettingsInput, logo LogoUpdate) (Settings, error)
}

// Handler serves org-scoped issuer settings (admin only), except the logo which
// any member may read so the admin preview loads.
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
	mux.Handle("GET /orgs/{slug}/issuer/settings", admin(respond.HandlerFunc(h.getSettings)))
	mux.Handle("PUT /orgs/{slug}/issuer/settings", admin(respond.HandlerFunc(h.putSettings)))
	mux.Handle("GET /orgs/{slug}/issuer/settings/logo", member(respond.HandlerFunc(h.serveLogo)))
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	settings, err := h.store.GetSettings(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("getting issuer settings: %w", err)
	}
	// Default the instance name to the org slug so the UI shows the effective
	// default before the org has saved anything.
	if !settings.Configured {
		settings.InstanceName = org.Slug
		settings.Enabled = true
	}
	settings.LogoURI = logoURL(org.Slug, settings)
	respond.JSON(w, r, http.StatusOK, settings)
	return nil
}

// putSettings saves the instance settings and applies the logo change carried by
// the multipart form: an uploaded "logo" file replaces the logo, a
// "removeLogo=true" field clears it, and neither leaves the existing logo
// untouched.
func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxLogoBytes+bodySlack)
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return apiError(http.StatusRequestEntityTooLarge, "payload_too_large", "the logo is too large")
		}
		return badRequest("invalid_body", "invalid multipart form")
	}

	instanceName := strings.TrimSpace(r.FormValue("instanceName"))
	if !instanceNamePattern.MatchString(instanceName) {
		return badRequest("invalid_input", "instanceName must be a lowercase slug (a-z, 0-9, hyphen)")
	}

	logo, err := parseLogoUpdate(r)
	if err != nil {
		return err
	}

	org := organization.OrgFromContext(r.Context())
	settings, err := h.store.Upsert(r.Context(), org.ID, SettingsInput{
		InstanceName: instanceName,
		DisplayName:  strings.TrimSpace(r.FormValue("displayName")),
		Enabled:      r.FormValue("enabled") == "true",
	}, logo)
	if err != nil {
		return fmt.Errorf("updating issuer settings: %w", err)
	}
	settings.LogoURI = logoURL(org.Slug, settings)
	respond.JSON(w, r, http.StatusOK, settings)
	return nil
}

// serveLogo streams the org's stored logo bytes with a locked-down response
// (see setLogoResponseHeaders). It backs the admin settings preview; the
// wallet-facing bundle embeds the logo as a data: URI instead.
func (h *Handler) serveLogo(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	logo, err := h.store.GetLogo(r.Context(), org.ID)
	if errors.Is(err, ErrNoLogo) {
		return apiError(http.StatusNotFound, "not_found", "no logo set")
	}
	if err != nil {
		return fmt.Errorf("getting issuer logo: %w", err)
	}

	setLogoResponseHeaders(w.Header(), logo.ContentType)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(logo.Bytes); err != nil {
		// The status and headers are already committed, so an error here can only
		// be logged, not turned into an API error response.
		slog.ErrorContext(r.Context(), "issuersettings: write logo body", slog.String("error", err.Error()))
	}
	return nil
}

// setLogoResponseHeaders locks the logo response down. The logo is
// admin-uploaded content served same-origin, so nosniff keeps the declared type
// authoritative and the sandbox + null-source CSP stop an uploaded SVG from
// running script if the URL is opened directly.
func setLogoResponseHeaders(h http.Header, contentType string) {
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	h.Set("Cache-Control", "private, max-age=300")
}

// parseLogoUpdate reads the logo intent from the multipart form. A "logo" file
// part replaces the logo (validated for size and image type); "removeLogo=true"
// clears it; otherwise the existing logo is kept.
func parseLogoUpdate(r *http.Request) (LogoUpdate, error) {
	file, _, err := r.FormFile(logoFormField)
	if errors.Is(err, http.ErrMissingFile) {
		if r.FormValue("removeLogo") == "true" {
			return LogoUpdate{Replace: true}, nil
		}
		return LogoUpdate{}, nil
	}
	if err != nil {
		return LogoUpdate{}, badRequest("invalid_body", "invalid logo upload")
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, MaxLogoBytes+1))
	if err != nil {
		return LogoUpdate{}, fmt.Errorf("reading uploaded logo: %w", err)
	}
	if len(data) == 0 {
		return LogoUpdate{}, badRequest("invalid_input", "the logo file is empty")
	}
	if len(data) > MaxLogoBytes {
		return LogoUpdate{}, apiError(http.StatusRequestEntityTooLarge, "payload_too_large", "the logo is too large")
	}
	contentType, ok := detectLogoType(data)
	if !ok {
		return LogoUpdate{}, badRequest("invalid_input", "the logo must be a PNG, JPEG, GIF, WebP or SVG image")
	}
	return LogoUpdate{Replace: true, Logo: Logo{Bytes: data, ContentType: contentType}}, nil
}

// detectLogoType sniffs the actual bytes (not the client-declared type) and
// returns the canonical MIME type for a supported image, or ok=false otherwise.
// Raster formats are recognised by http.DetectContentType; SVG (XML, which the
// sniffer reports as text) is matched separately.
func detectLogoType(data []byte) (string, bool) {
	switch sniff := http.DetectContentType(data); {
	case strings.HasPrefix(sniff, "image/png"):
		return "image/png", true
	case strings.HasPrefix(sniff, "image/jpeg"):
		return "image/jpeg", true
	case strings.HasPrefix(sniff, "image/gif"):
		return "image/gif", true
	case strings.HasPrefix(sniff, "image/webp"):
		return "image/webp", true
	}
	if looksLikeSVG(data) {
		return "image/svg+xml", true
	}
	return "", false
}

// looksLikeSVG reports whether the bytes open an SVG document, allowing a
// leading XML declaration or doctype before the <svg> root.
func looksLikeSVG(data []byte) bool {
	const sniffLen = 1024
	head := data
	if len(head) > sniffLen {
		head = head[:sniffLen]
	}
	head = bytes.ToLower(bytes.TrimSpace(head))
	if bytes.HasPrefix(head, []byte("<svg")) {
		return true
	}
	return bytes.HasPrefix(head, []byte("<?xml")) && bytes.Contains(head, []byte("<svg"))
}

// logoURL is the API path that serves an org's logo for the admin preview, or ""
// when none is stored. The updated-at timestamp is a cache-busting version so a
// replaced logo is re-fetched rather than served stale from the browser cache.
func logoURL(slug string, s Settings) string {
	if !s.HasLogo {
		return ""
	}
	version := "0"
	if s.UpdatedAt != nil {
		version = strconv.FormatInt(s.UpdatedAt.Unix(), 10)
	}
	return fmt.Sprintf("/api/v1/orgs/%s/issuer/settings/logo?v=%s", url.PathEscape(slug), version)
}

func apiError(status int, code, msg string) error {
	return &respond.APIError{Status: status, Code: code, Message: msg}
}

func badRequest(code, msg string) error {
	return apiError(http.StatusBadRequest, code, msg)
}
