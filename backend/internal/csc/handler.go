package csc

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
	BaseURLFor(ctx context.Context, orgID uuid.UUID) (string, error)
}

// tester probes a CSC endpoint's /info (implemented by *Client).
type tester interface {
	Info(ctx context.Context, baseURL string) (Info, error)
}

// Handler serves an org's CSC signing-provider settings and a connection test.
// Both reads and writes are org-admin only: this is provider configuration that
// holds a credential, and even the read says whether a secret is stored.
type Handler struct {
	store       settingsStore
	client      tester
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(store settingsStore, client tester, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{store: store, client: client, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}
	mux.Handle("GET /orgs/{slug}/csc/settings", admin(respond.HandlerFunc(h.getSettings)))
	mux.Handle("PUT /orgs/{slug}/csc/settings", admin(respond.HandlerFunc(h.putSettings)))
	mux.Handle("POST /orgs/{slug}/csc/test", admin(respond.HandlerFunc(h.postTest)))
}

// settingsResponse is the org's saved configuration plus the provider kinds this
// deployment can configure, so the settings screen renders from one request.
type settingsResponse struct {
	Settings
	ProviderKinds []ProviderKindInfo `json:"providerKinds"`
}

func newSettingsResponse(s Settings) settingsResponse {
	return settingsResponse{Settings: s, ProviderKinds: ProviderKinds()}
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	settings, err := h.store.GetSettings(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("getting csc settings: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, newSettingsResponse(settings))
	return nil
}

// settingsRequest replaces the whole configuration. ClientSecret is a pointer so a
// body that omits it (the admin only changed the base URL) is distinguishable from
// one that sends it empty, which is how the secret is cleared.
type settingsRequest struct {
	Enabled      bool         `json:"enabled"`
	ProviderKind ProviderKind `json:"providerKind"`
	BaseURL      string       `json:"baseUrl"`
	ClientID     string       `json:"clientId"`
	ClientSecret *string      `json:"clientSecret"`
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) error {
	in, err := parseSettingsRequest(r)
	if err != nil {
		return err
	}

	org := organization.OrgFromContext(r.Context())
	settings, err := h.store.Upsert(r.Context(), org.ID, in)
	if errors.Is(err, ErrNoEncryptionKey) {
		return &respond.APIError{
			Status:  http.StatusConflict,
			Code:    "no_encryption_key",
			Message: "this deployment has no CSC encryption key configured, so a client secret cannot be stored; set CSC_ENCRYPTION_KEY on the server",
		}
	}
	if err != nil {
		return fmt.Errorf("updating csc settings: %w", err)
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
	in, err := Normalize(SettingsInput(req))
	if err != nil {
		return SettingsInput{}, badRequest("invalid_input", err.Error())
	}
	return in, nil
}

// postTest probes the saved endpoint's /csc/v2/info. It answers with the
// provider's own name and spec version on success, so the admin sees the
// connection reached the QTSP they meant.
func (h *Handler) postTest(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	baseURL, err := h.store.BaseURLFor(r.Context(), org.ID)
	if errors.Is(err, ErrNotConfigured) {
		return &respond.APIError{Status: http.StatusConflict, Code: "not_configured", Message: "save a CSC base URL before testing the connection"}
	}
	if err != nil {
		return fmt.Errorf("reading csc base url: %w", err)
	}

	ctx, cancel := context.WithTimeout(r.Context(), DefaultTestTimeout)
	defer cancel()

	var testErr *TestError
	info, err := h.client.Info(ctx, baseURL)
	switch {
	case errors.As(err, &testErr):
		// The endpoint (or the network) did not answer usably. The admin is the one
		// who can fix the URL, so they get the reason — which carries no far-side bytes.
		return &respond.APIError{Status: http.StatusBadGateway, Code: "test_failed", Message: testErr.Reason}
	case err != nil:
		return fmt.Errorf("testing csc connection: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, info)
	return nil
}

func badRequest(code, msg string) error {
	return &respond.APIError{Status: http.StatusBadRequest, Code: code, Message: msg}
}
