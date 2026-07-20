package wscawallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/wsca"
)

// minSecretDigits mirrors secdsa.MinPINLength (the wallet-provider PIN policy):
// a WSCA secret is at least this many ASCII digits. walletmobile enforces it
// server-side; we pre-check to return a clean 400 instead of a generic failure.
const minSecretDigits = 5

// validSecret reports whether s satisfies the WSCA PIN policy (>= minSecretDigits
// ASCII digits), matching secdsa.IsValidPIN.
func validSecret(s string) bool {
	if len(s) < minSecretDigits {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// activatorService is the Activator surface the handler needs; an interface so
// the HTTP layer is testable without a live WSCA.
type activatorService interface {
	Configured() bool
	Activate(ctx context.Context, orgID uuid.UUID, secret string) (wsca.Account, error)
	Rotate(ctx context.Context, orgID uuid.UUID, currentSecret, newSecret string) (wsca.Account, error)
	Status(ctx context.Context, orgID uuid.UUID) (wsca.Account, error)
}

// Handler serves the org-admin WSCA holder-wallet lifecycle API (activate the
// org's wallet-provider account, rotate its secret, read status). All routes are
// org-admin only — activation/rotation is the human-in-the-loop moment for the
// otherwise-autonomous business wallet (see .ai/features/wsca-holder-binding.md).
type Handler struct {
	activator   activatorService
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(activator activatorService, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{activator: activator, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}
	mux.Handle("GET /orgs/{slug}/wsca", admin(respond.HandlerFunc(h.status)))
	mux.Handle("POST /orgs/{slug}/wsca/activate", admin(respond.HandlerFunc(h.activate)))
	mux.Handle("POST /orgs/{slug}/wsca/rotate", admin(respond.HandlerFunc(h.rotate)))
}

// statusView reports whether the deployment can back holder keys with WSCA
// (Configured) and whether this org has activated its wallet.
type statusView struct {
	Configured bool          `json:"configured"`
	Activated  bool          `json:"activated"`
	Account    *wsca.Account `json:"account,omitempty"`
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	view := statusView{Configured: h.activator.Configured()}
	acct, err := h.activator.Status(r.Context(), org.ID)
	switch {
	case errors.Is(err, wsca.ErrNotActivated):
		// Not activated yet — leave Activated false.
	case err != nil:
		return fmt.Errorf("wsca status: %w", err)
	default:
		view.Activated = true
		view.Account = &acct
	}
	respond.JSON(w, r, http.StatusOK, view)
	return nil
}

type activateRequest struct {
	Secret string `json:"secret"`
}

func (h *Handler) activate(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	var in activateRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	if !validSecret(in.Secret) {
		return badRequest("invalid_secret", fmt.Sprintf("secret must be at least %d digits", minSecretDigits))
	}
	acct, err := h.activator.Activate(r.Context(), org.ID, in.Secret)
	switch {
	case errors.Is(err, ErrAlreadyActivated):
		return conflict("already_activated", "the organization wallet is already activated; rotate the secret instead")
	case errors.Is(err, wsca.ErrNotConfigured):
		return notConfigured()
	case err != nil:
		return fmt.Errorf("wsca activate: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, acct)
	return nil
}

type rotateRequest struct {
	CurrentSecret string `json:"currentSecret"`
	NewSecret     string `json:"newSecret"`
}

func (h *Handler) rotate(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	var in rotateRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	if in.CurrentSecret == "" {
		return badRequest("missing_current_secret", "currentSecret is required")
	}
	if !validSecret(in.NewSecret) {
		return badRequest("invalid_secret", fmt.Sprintf("newSecret must be at least %d digits", minSecretDigits))
	}
	acct, err := h.activator.Rotate(r.Context(), org.ID, in.CurrentSecret, in.NewSecret)
	switch {
	case errors.Is(err, wsca.ErrNotActivated):
		return conflict("not_activated", "the organization wallet is not activated; activate it first")
	case errors.Is(err, wsca.ErrNotConfigured):
		return notConfigured()
	case err != nil:
		return fmt.Errorf("wsca rotate: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, acct)
	return nil
}

func badRequest(code, msg string) error {
	return &respond.APIError{Status: http.StatusBadRequest, Code: code, Message: msg}
}

func conflict(code, msg string) error {
	return &respond.APIError{Status: http.StatusConflict, Code: code, Message: msg}
}

func notConfigured() error {
	return &respond.APIError{Status: http.StatusServiceUnavailable, Code: "wsca_not_configured", Message: "WSCA-backed holder binding is not configured on this deployment"}
}
