package slackchannel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

type settingsStore interface {
	GetSettings(ctx context.Context, orgID uuid.UUID) (Settings, error)
	Upsert(ctx context.Context, orgID uuid.UUID, in SettingsInput) (Settings, error)
}

// testSender posts the specimen message (implemented by *Channel).
type testSender interface {
	SendTest(ctx context.Context, orgID uuid.UUID, orgName string) error
}

// Handler serves an org's Slack settings. Both reads and writes are org-admin
// only: this is who-gets-told-what configuration, and even the read says whether a
// webhook is in place.
type Handler struct {
	store       settingsStore
	channel     testSender
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(store settingsStore, channel testSender, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{store: store, channel: channel, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}
	mux.Handle("GET /orgs/{slug}/slack/settings", admin(respond.HandlerFunc(h.getSettings)))
	mux.Handle("PUT /orgs/{slug}/slack/settings", admin(respond.HandlerFunc(h.putSettings)))
	mux.Handle("POST /orgs/{slug}/slack/test", admin(respond.HandlerFunc(h.sendTest)))
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	settings, err := h.store.GetSettings(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("getting slack settings: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, settings)
	return nil
}

// settingsRequest is an upsert of the Slack configuration. WebhookURL is a pointer
// so a body that omits it (the admin only toggled delivery off) is distinguishable
// from one that sends it empty, which is how the webhook is cleared.
type settingsRequest struct {
	WebhookURL *string `json:"webhookUrl"`
	Enabled    bool    `json:"enabled"`
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) error {
	in, err := parseSettingsRequest(r)
	if err != nil {
		return err
	}

	org := organization.OrgFromContext(r.Context())
	settings, err := h.store.Upsert(r.Context(), org.ID, in)
	if errors.Is(err, ErrNoEncryptionKey) {
		// A deployment without a Slack key cannot hold the secret at all, and the
		// admin pasting the URL is the one who finds out. Say so instead of 500ing.
		return apiError(http.StatusConflict, "no_encryption_key",
			"this deployment has no Slack encryption key, so a webhook URL cannot be stored")
	} else if err != nil {
		return fmt.Errorf("updating slack settings: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, settings)
	return nil
}

// parseSettingsRequest decodes and validates the body. A submitted webhook URL is
// normalized here, so an unusable one is a 400 at save time rather than a delivery
// that quietly never arrives.
func parseSettingsRequest(r *http.Request) (SettingsInput, error) {
	var req settingsRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return SettingsInput{}, badRequest("invalid_body", "invalid request body")
	}
	in := SettingsInput{Enabled: req.Enabled}
	if req.WebhookURL != nil {
		normalized, err := NormalizeWebhookURL(*req.WebhookURL)
		if err != nil {
			// The message repeats nothing of the value: it is a secret, and it is the
			// shape that is wrong, not the particular characters.
			return SettingsInput{}, badRequest("invalid_input",
				"webhookUrl must be an https URL at "+webhookHost)
		}
		in.WebhookURL = &normalized
	}
	return in, nil
}

func (h *Handler) sendTest(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	var delivery *DeliveryError
	switch err := h.channel.SendTest(r.Context(), org.ID, org.Name); {
	case errors.Is(err, ErrNotConfigured):
		return apiError(http.StatusConflict, "not_configured",
			"Slack is not configured or is switched off for this organization")
	case errors.As(err, &delivery):
		// Slack (or the network) refused it. The admin is the one who can fix the
		// webhook, so they get the reason rather than only the log.
		return apiError(http.StatusBadGateway, "webhook_failed", delivery.Reason)
	case err != nil:
		return fmt.Errorf("sending slack test notification: %w", err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func apiError(status int, code, msg string) error {
	return &respond.APIError{Status: status, Code: code, Message: msg}
}

func badRequest(code, msg string) error {
	return apiError(http.StatusBadRequest, code, msg)
}
