package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/provisioner"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

type configStore interface {
	GetSettings(ctx context.Context, orgID uuid.UUID) (Settings, error)
	Save(ctx context.Context, orgID uuid.UUID, in SettingsInput) (Settings, error)
}

type syncer interface {
	Sync(ctx context.Context, orgID uuid.UUID) (Result, error)
}

// Handler serves an organisation's provisioning configuration and the run-now
// button. Everything here is org-admin only: it holds a directory credential and
// it can add and remove members.
type Handler struct {
	store       configStore
	sync        syncer
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(store configStore, sync syncer, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{store: store, sync: sync, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}
	mux.Handle("GET /orgs/{slug}/provisioning/settings", admin(respond.HandlerFunc(h.getSettings)))
	mux.Handle("PUT /orgs/{slug}/provisioning/settings", admin(respond.HandlerFunc(h.putSettings)))
	mux.Handle("POST /orgs/{slug}/provisioning/sync", admin(respond.HandlerFunc(h.postSync)))
}

// settingsResponse is the organisation's saved configuration plus the sources
// this deployment can configure, so the settings screen renders from one request.
type settingsResponse struct {
	Settings
	Sources []provisioner.SourceID `json:"sources"`
}

func newSettingsResponse(s Settings) settingsResponse {
	return settingsResponse{Settings: s, Sources: Sources()}
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	settings, err := h.store.GetSettings(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("getting provisioning settings: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, newSettingsResponse(settings))
	return nil
}

// settingsRequest replaces the whole configuration.
//
// ClientSecret is a pointer so a body that omits the key keeps the stored secret,
// while sending it empty clears it. AdminGroupIDs is a pointer for the same
// reason the notification subscriptions document is: omitting it is a malformed
// request, and accepting it would make a typo a silent "nobody is an admin".
type settingsRequest struct {
	Enabled       bool                 `json:"enabled"`
	Source        provisioner.SourceID `json:"source"`
	TenantID      string               `json:"tenantId"`
	ClientID      string               `json:"clientId"`
	ClientSecret  *string              `json:"clientSecret"`
	GroupID       string               `json:"groupId"`
	AdminGroupIDs *[]string            `json:"adminGroupIds"`
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) error {
	in, err := parseSettingsRequest(r)
	if err != nil {
		return err
	}

	org := organization.OrgFromContext(r.Context())
	settings, err := h.store.Save(r.Context(), org.ID, in)
	if err != nil {
		return fmt.Errorf("updating provisioning settings: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, newSettingsResponse(settings))
	return nil
}

func parseSettingsRequest(r *http.Request) (SettingsInput, error) {
	var req settingsRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return SettingsInput{}, badRequest("invalid_body", "invalid request body")
	}
	if req.AdminGroupIDs == nil {
		return SettingsInput{}, badRequest("invalid_input", "adminGroupIds is required")
	}
	in, err := Normalize(SettingsInput{
		Enabled:       req.Enabled,
		Source:        req.Source,
		TenantID:      req.TenantID,
		ClientID:      req.ClientID,
		ClientSecret:  req.ClientSecret,
		GroupID:       req.GroupID,
		AdminGroupIDs: *req.AdminGroupIDs,
	})
	if err != nil {
		return SettingsInput{}, badRequest("invalid_input", err.Error())
	}
	return in, nil
}

// postSync runs a sync now and answers with what it did. It is synchronous: an
// admin who just fixed a credential wants the outcome, not a job id, and the
// directory read is a handful of paged HTTP calls.
func (h *Handler) postSync(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	result, err := h.sync.Sync(r.Context(), org.ID)
	switch {
	case errors.Is(err, ErrNotConfigured):
		return &respond.APIError{Status: http.StatusConflict, Code: "not_configured", Message: "provisioning is not configured for this organization"}
	case errors.Is(err, ErrDisabled):
		return &respond.APIError{Status: http.StatusConflict, Code: "disabled", Message: "provisioning is disabled for this organization"}
	case errors.Is(err, ErrUnknownSource):
		return &respond.APIError{Status: http.StatusConflict, Code: "unknown_source", Message: "this deployment has no driver for the configured source"}
	case errors.Is(err, ErrEmptyDirectory):
		return &respond.APIError{Status: http.StatusBadGateway, Code: "empty_directory", Message: "the source returned no accounts; nothing was changed"}
	case errors.Is(err, provisioner.ErrIncompleteConfig):
		return &respond.APIError{Status: http.StatusConflict, Code: "incomplete_config", Message: "the source configuration is incomplete"}
	case err != nil:
		// The source is somebody else's system, so a failure to reach it is a
		// gateway error rather than ours. The reason is on the settings row
		// (lastRunError), which is where the screen shows it.
		return &respond.APIError{Status: http.StatusBadGateway, Code: "sync_failed", Message: "the directory sync failed; see the last run error"}
	}
	respond.JSON(w, r, http.StatusOK, result)
	return nil
}

func badRequest(code, msg string) error {
	return &respond.APIError{Status: http.StatusBadRequest, Code: code, Message: msg}
}
