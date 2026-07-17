package issuersettings

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

// instanceNamePattern constrains an issuer instance name to a safe URL path
// segment (it becomes the {instance} segment of the hosted issuer URL and a
// filename in the ops config repo).
var instanceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type settingsStore interface {
	GetSettings(ctx context.Context, orgID uuid.UUID) (Settings, error)
	Upsert(ctx context.Context, orgID uuid.UUID, in SettingsInput) (Settings, error)
}

// Handler serves org-scoped issuer settings (admin only).
type Handler struct {
	store       settingsStore
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(store settingsStore, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{store: store, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}
	mux.Handle("GET /orgs/{slug}/issuer/settings", admin(respond.HandlerFunc(h.getSettings)))
	mux.Handle("PUT /orgs/{slug}/issuer/settings", admin(respond.HandlerFunc(h.putSettings)))
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
	respond.JSON(w, r, http.StatusOK, settings)
	return nil
}

type settingsRequest struct {
	InstanceName string `json:"instanceName"`
	DisplayName  string `json:"displayName"`
	LogoURI      string `json:"logoUri"`
	Enabled      bool   `json:"enabled"`
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) error {
	var req settingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	req.InstanceName = strings.TrimSpace(req.InstanceName)
	if !instanceNamePattern.MatchString(req.InstanceName) {
		return badRequest("invalid_input", "instanceName must be a lowercase slug (a-z, 0-9, hyphen)")
	}

	org := organization.OrgFromContext(r.Context())
	settings, err := h.store.Upsert(r.Context(), org.ID, SettingsInput{
		InstanceName: req.InstanceName,
		DisplayName:  strings.TrimSpace(req.DisplayName),
		LogoURI:      strings.TrimSpace(req.LogoURI),
		Enabled:      req.Enabled,
	})
	if err != nil {
		return fmt.Errorf("updating issuer settings: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, settings)
	return nil
}

func badRequest(code, msg string) error {
	return &respond.APIError{Status: http.StatusBadRequest, Code: code, Message: msg}
}
