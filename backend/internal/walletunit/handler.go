package walletunit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

const statusListContentType = "application/statuslist+jwt"

type unitStore interface {
	Summary(ctx context.Context, orgID uuid.UUID) (Summary, error)
	Revoke(ctx context.Context, orgID uuid.UUID, reason string) (Unit, error)
	Activate(ctx context.Context, orgID uuid.UUID) (Unit, error)
}

type statusLists interface {
	StatusList(ctx context.Context, list int64) (string, error)
}

// Handler serves the org-admin wallet unit routes and the public status lists.
type Handler struct {
	units       unitStore
	lists       statusLists
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(units unitStore, lists statusLists, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{units: units, lists: lists, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}
	mux.Handle("GET /orgs/{slug}/wallet-unit", admin(respond.HandlerFunc(h.summary)))
	mux.Handle("POST /orgs/{slug}/wallet-unit/revoke", admin(respond.HandlerFunc(h.revoke)))
	mux.Handle("POST /orgs/{slug}/wallet-unit/activate", admin(respond.HandlerFunc(h.activate)))
	mux.HandleFunc("GET /wallet-unit/status-lists/{list}", h.statusList)
}

// summaryView is the latest unit, or Issued false when the org has none yet.
type summaryView struct {
	Issued  bool     `json:"issued"`
	Summary *Summary `json:"summary,omitempty"`
}

func (h *Handler) summary(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	sum, err := h.units.Summary(r.Context(), org.ID)
	switch {
	case errors.Is(err, ErrNoActiveUnit):
		respond.JSON(w, r, http.StatusOK, summaryView{})
		return nil
	case err != nil:
		return fmt.Errorf("wallet unit summary: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, summaryView{Issued: true, Summary: &sum})
	return nil
}

type revokeRequest struct {
	Reason string `json:"reason"`
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	var in revokeRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return apiError(http.StatusBadRequest, "invalid_body", "invalid request body")
	}
	unit, err := h.units.Revoke(r.Context(), org.ID, in.Reason)
	switch {
	case errors.Is(err, ErrInvalidReason):
		return apiError(http.StatusBadRequest, "invalid_reason", "reason must be owner_request or key_compromise")
	case errors.Is(err, ErrNoActiveUnit):
		return apiError(http.StatusConflict, "no_active_unit", "the organization has no active wallet unit")
	case err != nil:
		return fmt.Errorf("wallet unit revoke: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, unit)
	return nil
}

func (h *Handler) activate(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	unit, err := h.units.Activate(r.Context(), org.ID)
	switch {
	case errors.Is(err, ErrUnitActive):
		return apiError(http.StatusConflict, "unit_active", "the organization already has an active wallet unit")
	case err != nil:
		return fmt.Errorf("wallet unit activate: %w", err)
	}
	respond.JSON(w, r, http.StatusCreated, unit)
	return nil
}

func (h *Handler) statusList(w http.ResponseWriter, r *http.Request) {
	list, err := strconv.ParseInt(r.PathValue("list"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	token, err := h.lists.StatusList(r.Context(), list)
	if errors.Is(err, ErrUnknownList) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "walletunit: status list", slog.Int64("list", list), slog.Any("error", err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", statusListContentType)
	if _, err := w.Write([]byte(token)); err != nil {
		slog.ErrorContext(r.Context(), "walletunit: write status list", slog.Any("error", err))
	}
}

func apiError(status int, code, msg string) error {
	return &respond.APIError{Status: status, Code: code, Message: msg}
}
