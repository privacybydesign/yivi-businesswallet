package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/mailer"
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

// templateStore is the tenant-editing surface of the mail catalogue (implemented
// by *Store). Kinds and locales themselves are code, so there is no create.
type templateStore interface {
	ListTemplates(ctx context.Context, orgID uuid.UUID) ([]TemplateOverride, error)
	GetTemplate(ctx context.Context, orgID uuid.UUID, kind Kind, locale Locale) (TemplateOverride, bool, error)
	SaveTemplate(ctx context.Context, orgID uuid.UUID, kind Kind, locale Locale, tpl Template) (TemplateOverride, error)
	DeleteTemplate(ctx context.Context, orgID uuid.UUID, kind Kind, locale Locale) (bool, error)
}

type mailService interface {
	SendSpecimen(ctx context.Context, orgID uuid.UUID, kind Kind, locale Locale, to, orgName string) error
	Preview(ctx context.Context, orgID uuid.UUID, kind Kind, locale Locale, tpl *Template, orgName string) (Body, error)
}

// orgMailStore is everything the handler persists through, implemented by *Store.
type orgMailStore interface {
	settingsStore
	templateStore
}

// Handler serves org-scoped SMTP settings and mail templates (admin only).
type Handler struct {
	store       orgMailStore
	service     mailService
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(store orgMailStore, service mailService, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{store: store, service: service, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}
	mux.Handle("GET /orgs/{slug}/email/settings", admin(respond.HandlerFunc(h.getSettings)))
	mux.Handle("PUT /orgs/{slug}/email/settings", admin(respond.HandlerFunc(h.putSettings)))
	mux.Handle("POST /orgs/{slug}/email/test", admin(respond.HandlerFunc(h.sendTest)))
	h.registerTemplateRoutes(mux, admin)
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

// settingsRequest replaces an org's SMTP configuration. Password and
// ClientSecret are pointers so a body that omits the key keeps the stored secret,
// while sending it empty clears it. AuthMechanism may be omitted, and then means
// mailer.AuthPlain — a client written before XOAUTH2 existed keeps working.
type settingsRequest struct {
	Host          string               `json:"host"`
	Port          int                  `json:"port"`
	Username      string               `json:"username"`
	Password      *string              `json:"password"`
	AuthMechanism mailer.AuthMechanism `json:"authMechanism"`
	TenantID      string               `json:"tenantId"`
	ClientID      string               `json:"clientId"`
	ClientSecret  *string              `json:"clientSecret"`
	FromName      string               `json:"fromName"`
	FromAddress   string               `json:"fromAddress"`
	Enabled       bool                 `json:"enabled"`
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) error {
	var req settingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}

	org := organization.OrgFromContext(r.Context())
	stored, err := h.store.GetSettings(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("getting email settings: %w", err)
	}
	in, err := parseSettingsRequest(req, stored)
	if err != nil {
		return err
	}

	settings, err := h.store.Upsert(r.Context(), org.ID, in)
	if err != nil {
		return fmt.Errorf("updating email settings: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, settings)
	return nil
}

// parseSettingsRequest normalises and validates a settings body. stored is the
// org's current settings, needed because "keep the stored secret" is a valid way
// to satisfy the credential requirement of an enabled XOAUTH2 configuration.
func parseSettingsRequest(req settingsRequest, stored Settings) (SettingsInput, error) {
	in := SettingsInput{
		Host:          strings.TrimSpace(req.Host),
		Port:          req.Port,
		Username:      strings.TrimSpace(req.Username),
		Password:      req.Password,
		AuthMechanism: req.AuthMechanism,
		TenantID:      strings.TrimSpace(req.TenantID),
		ClientID:      strings.TrimSpace(req.ClientID),
		ClientSecret:  req.ClientSecret,
		FromName:      strings.TrimSpace(req.FromName),
		FromAddress:   strings.TrimSpace(req.FromAddress),
		Enabled:       req.Enabled,
	}
	if in.AuthMechanism == "" {
		in.AuthMechanism = mailer.AuthPlain
	}
	if !mailer.KnownMechanism(in.AuthMechanism) {
		return SettingsInput{}, badRequest("invalid_input", "authMechanism must be one of the supported SMTP auth mechanisms")
	}
	if in.Host == "" {
		return SettingsInput{}, badRequest("invalid_input", "host is required")
	}
	if in.Port < minPort || in.Port > maxPort {
		return SettingsInput{}, badRequest("invalid_input", "port must be between 1 and 65535")
	}
	if in.FromAddress == "" {
		return SettingsInput{}, badRequest("invalid_input", "fromAddress is required")
	}
	// Only an enabled XOAUTH2 configuration has to be complete: an org part-way
	// through setting one up saves it switched off, the same latitude the
	// directory-sync screen gives. Once it is on, an incomplete credential only
	// fails at the first send, which is a mail nobody receives rather than a
	// message here.
	if in.AuthMechanism == mailer.AuthXOAuth2 && in.Enabled {
		if in.TenantID == "" || in.ClientID == "" {
			return SettingsInput{}, badRequest("invalid_input", "tenantId and clientId are required for xoauth2")
		}
		if !keepsSecret(in.ClientSecret, stored.HasClientSecret) {
			return SettingsInput{}, badRequest("invalid_input", "clientSecret is required for xoauth2")
		}
	}
	return in, nil
}

// keepsSecret reports whether the request leaves a usable secret in place: it
// either sets a new one, or omits the field while one is already stored.
func keepsSecret(input *string, hasStored bool) bool {
	if input == nil {
		return hasStored
	}
	return *input != ""
}

// testRequest asks for a specimen message. Kind and Locale are optional: without
// them the SMTP self-test goes out in the deployment's default language, which is
// what the "does my SMTP work" button has always sent. With them an admin gets a
// real specimen of one cause, in one language, rendered from their own template.
type testRequest struct {
	To     string `json:"to"`
	Kind   string `json:"kind,omitempty"`
	Locale string `json:"locale,omitempty"`
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
	kind := KindSMTPTest
	if req.Kind != "" {
		parsed, ok := parseKind(req.Kind)
		if !ok {
			return badRequest("invalid_input", "kind must be one of the mail template kinds")
		}
		kind = parsed
	}
	// An empty locale leaves the choice to the service's deployment default.
	var locale Locale
	if req.Locale != "" {
		parsed, ok := ParseLocale(req.Locale)
		if !ok {
			return badRequest("invalid_input", "locale must be one of the supported mail locales")
		}
		locale = parsed
	}

	org := organization.OrgFromContext(r.Context())
	if err := h.service.SendSpecimen(r.Context(), org.ID, kind, locale, req.To, org.Name); errors.Is(err, ErrNotConfigured) {
		return &respond.APIError{Status: http.StatusConflict, Code: "not_configured", Message: "SMTP is not configured or is disabled"}
	} else if err != nil {
		return fmt.Errorf("sending test email: %w", err)
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
