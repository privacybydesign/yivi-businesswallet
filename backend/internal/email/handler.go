package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

const (
	minPort = 1
	maxPort = 65535
)

type settingsStore interface {
	GetSettings(ctx context.Context, orgID uuid.UUID) (Settings, error)
	Upsert(ctx context.Context, orgID uuid.UUID, in SettingsInput) (Settings, error)
}

type tester interface {
	SendTest(ctx context.Context, orgID uuid.UUID, to string) error
}

// Handler serves org-scoped SMTP settings (admin only).
type Handler struct {
	store       settingsStore
	service     tester
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(store settingsStore, service tester, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{store: store, service: service, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}
	mux.Handle("GET /orgs/{slug}/email/settings", admin(respond.HandlerFunc(h.getSettings)))
	mux.Handle("PUT /orgs/{slug}/email/settings", admin(respond.HandlerFunc(h.putSettings)))
	mux.Handle("POST /orgs/{slug}/email/test", admin(respond.HandlerFunc(h.sendTest)))
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	settings, err := h.store.GetSettings(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("getting email settings: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, settings)
	return nil
}

type settingsRequest struct {
	Host        string  `json:"host"`
	Port        int     `json:"port"`
	Username    string  `json:"username"`
	Password    *string `json:"password"`
	FromName    string  `json:"fromName"`
	FromAddress string  `json:"fromAddress"`
	Enabled     bool    `json:"enabled"`
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) error {
	var req settingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	req.Host = strings.TrimSpace(req.Host)
	req.FromAddress = strings.TrimSpace(req.FromAddress)
	if req.Host == "" {
		return badRequest("invalid_input", "host is required")
	}
	if req.Port < minPort || req.Port > maxPort {
		return badRequest("invalid_input", "port must be between 1 and 65535")
	}
	if req.FromAddress == "" {
		return badRequest("invalid_input", "fromAddress is required")
	}

	org := organization.OrgFromContext(r.Context())
	settings, err := h.store.Upsert(r.Context(), org.ID, SettingsInput{
		Host: req.Host, Port: req.Port, Username: strings.TrimSpace(req.Username),
		Password: req.Password, FromName: strings.TrimSpace(req.FromName),
		FromAddress: req.FromAddress, Enabled: req.Enabled,
	})
	if err != nil {
		return fmt.Errorf("updating email settings: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, settings)
	return nil
}

type testRequest struct {
	To string `json:"to"`
}

func (h *Handler) sendTest(w http.ResponseWriter, r *http.Request) error {
	var req testRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	req.To = strings.TrimSpace(req.To)
	if req.To == "" {
		return badRequest("invalid_input", "to is required")
	}

	org := organization.OrgFromContext(r.Context())
	if err := h.service.SendTest(r.Context(), org.ID, req.To); errors.Is(err, ErrNotConfigured) {
		return &respond.APIError{Status: http.StatusConflict, Code: "not_configured", Message: "SMTP is not configured or is disabled"}
	} else if err != nil {
		return fmt.Errorf("sending test email: %w", err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func badRequest(code, msg string) error {
	return &respond.APIError{Status: http.StatusBadRequest, Code: code, Message: msg}
}
