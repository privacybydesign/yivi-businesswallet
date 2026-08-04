package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

type settingsStore interface {
	GetSettings(ctx context.Context, orgID uuid.UUID) (Settings, error)
	Save(ctx context.Context, orgID uuid.UUID, in SettingsInput) (Settings, error)
}

// Handler serves an org's notification subscriptions. Both reads and writes are
// org-admin only: unlike the theme, this is not something the app renders for
// every member, it is who-gets-told-what configuration.
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
	mux.Handle("GET /orgs/{slug}/notifications/settings", admin(respond.HandlerFunc(h.getSettings)))
	mux.Handle("PUT /orgs/{slug}/notifications/settings", admin(respond.HandlerFunc(h.putSettings)))
}

// settingsResponse is the org's saved subscriptions plus the catalog they are
// chosen from, so the settings screen renders from one request.
type settingsResponse struct {
	Settings
	Events   []CatalogEntry `json:"events"`
	Channels []ChannelID    `json:"channels"`
}

func newSettingsResponse(s Settings) settingsResponse {
	return settingsResponse{Settings: s, Events: Catalog(), Channels: Channels()}
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	settings, err := h.store.GetSettings(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("getting notification settings: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, newSettingsResponse(settings))
	return nil
}

// settingsRequest replaces the whole subscription document: an event the request
// leaves out is unsubscribed. There is no partial update — the screen edits the
// full set of checkboxes and saves it as one.
//
// Subscriptions is a pointer so a body that omits the key is distinguishable from
// one that sends it empty. Both would normalize to the same empty document, but
// only the second is someone asking to unsubscribe from everything; the first is
// a malformed request, and accepting it would make a typo a silent full wipe.
type settingsRequest struct {
	Subscriptions *map[string][]ChannelID `json:"subscriptions"`
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) error {
	in, err := parseSettingsRequest(r)
	if err != nil {
		return err
	}

	org := organization.OrgFromContext(r.Context())
	settings, err := h.store.Save(r.Context(), org.ID, in)
	if err != nil {
		return fmt.Errorf("updating notification settings: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, newSettingsResponse(settings))
	return nil
}

// parseSettingsRequest decodes and validates the body into a normalized input.
func parseSettingsRequest(r *http.Request) (SettingsInput, error) {
	var req settingsRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return SettingsInput{}, badRequest("invalid_body", "invalid request body")
	}
	if req.Subscriptions == nil {
		return SettingsInput{}, badRequest("invalid_input", "subscriptions is required")
	}
	subs, err := Normalize(*req.Subscriptions)
	if err != nil {
		return SettingsInput{}, badRequest("invalid_input", err.Error())
	}
	return SettingsInput{Subscriptions: subs}, nil
}

func badRequest(code, msg string) error {
	return &respond.APIError{Status: http.StatusBadRequest, Code: code, Message: msg}
}
