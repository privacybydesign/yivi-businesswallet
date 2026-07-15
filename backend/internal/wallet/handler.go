package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// walletService is the surface the handler depends on.
type walletService interface {
	OpenWallet(ctx context.Context, requestorUserID uuid.UUID, kvkNumber string, pid registryprovider.PID) (Instance, error)
	GetInstance(ctx context.Context, id uuid.UUID) (Instance, error)
	WalletForOrg(ctx context.Context, orgID uuid.UUID) (Instance, error)
	Representations(ctx context.Context, orgID uuid.UUID) ([]Representation, error)
	ClaimRepresentation(ctx context.Context, orgID, repID, userID uuid.UUID) error
	Suspend(ctx context.Context, orgID uuid.UUID) (Instance, error)
	Revoke(ctx context.Context, orgID uuid.UUID) (Instance, error)
}

// Handler serves the central "open a wallet" API plus the org-scoped wallet and
// representation routes. Central routes are behind auth only (no org exists yet);
// org routes compose requireUser + authorize like the other org-scoped slices.
type Handler struct {
	service     walletService
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(service walletService, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{service: service, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	orgScoped := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(next))
	}
	orgAdmin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}

	// Central (slug-free): open a wallet and poll its bootstrap status.
	mux.Handle("POST /wallet", h.requireUser(respond.HandlerFunc(h.open)))
	mux.Handle("GET /wallet/{id}", h.requireUser(respond.HandlerFunc(h.getInstance)))

	// Org-scoped.
	mux.Handle("GET /orgs/{slug}/wallet", orgScoped(respond.HandlerFunc(h.getForOrg)))
	mux.Handle("GET /orgs/{slug}/wallet/representations", orgScoped(respond.HandlerFunc(h.listRepresentations)))
	mux.Handle("POST /orgs/{slug}/wallet/representations/{id}/claim", orgScoped(respond.HandlerFunc(h.claim)))
	mux.Handle("POST /orgs/{slug}/wallet/suspend", orgAdmin(respond.HandlerFunc(h.suspend)))
	mux.Handle("POST /orgs/{slug}/wallet/revoke", orgAdmin(respond.HandlerFunc(h.revoke)))
}

type openRequest struct {
	KVKNumber string `json:"kvkNumber"`
}

func (h *Handler) open(w http.ResponseWriter, r *http.Request) error {
	var req openRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	req.KVKNumber = strings.TrimSpace(req.KVKNumber)
	if req.KVKNumber == "" {
		return badRequest("invalid_input", "kvkNumber is required")
	}

	u := auth.UserFromContext(r.Context())
	// TODO(wallet-bootstrap): populate the PID from the requester's OpenID4VP
	// identity disclosure (passport/id-card). See .ai/features/auth-openid4vp.md.
	pid := registryprovider.PID{}

	in, err := h.service.OpenWallet(r.Context(), u.ID, req.KVKNumber, pid)
	if errors.Is(err, ErrRegistrationInProgress) {
		return &respond.APIError{Status: http.StatusConflict, Code: "registration_in_progress", Message: "a registration is already in progress for this company"}
	}
	if err != nil {
		return mapError(err, "opening wallet")
	}
	respond.JSON(w, r, http.StatusAccepted, in)
	return nil
}

func (h *Handler) getInstance(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid wallet id")
	}
	// TODO(wallet-bootstrap): scope the lookup to the requester so one user
	// cannot poll another user's registration.
	in, err := h.service.GetInstance(r.Context(), id)
	if err != nil {
		return mapError(err, "getting wallet")
	}
	respond.JSON(w, r, http.StatusOK, in)
	return nil
}

func (h *Handler) getForOrg(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	in, err := h.service.WalletForOrg(r.Context(), org.ID)
	if err != nil {
		return mapError(err, "getting org wallet")
	}
	respond.JSON(w, r, http.StatusOK, in)
	return nil
}

func (h *Handler) listRepresentations(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	reps, err := h.service.Representations(r.Context(), org.ID)
	if err != nil {
		return mapError(err, "listing representations")
	}
	respond.JSON(w, r, http.StatusOK, reps)
	return nil
}

func (h *Handler) claim(w http.ResponseWriter, r *http.Request) error {
	repID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid representation id")
	}
	org := organization.OrgFromContext(r.Context())
	u := auth.UserFromContext(r.Context())
	if err := h.service.ClaimRepresentation(r.Context(), org.ID, repID, u.ID); err != nil {
		return mapError(err, "claiming representation")
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) suspend(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	in, err := h.service.Suspend(r.Context(), org.ID)
	if err != nil {
		return mapError(err, "suspending wallet")
	}
	respond.JSON(w, r, http.StatusOK, in)
	return nil
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	in, err := h.service.Revoke(r.Context(), org.ID)
	if err != nil {
		return mapError(err, "revoking wallet")
	}
	respond.JSON(w, r, http.StatusOK, in)
	return nil
}

func badRequest(code, msg string) error {
	return &respond.APIError{Status: http.StatusBadRequest, Code: code, Message: msg}
}

// mapError translates domain sentinels to API errors; ErrNotImplemented surfaces
// as 501 so unbuilt seams are visible rather than a generic 500.
func mapError(err error, action string) error {
	switch {
	case errors.Is(err, ErrNotImplemented):
		return &respond.APIError{Status: http.StatusNotImplemented, Code: "not_implemented", Message: "not implemented yet"}
	case errors.Is(err, ErrInstanceNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "wallet_not_found", Message: "wallet not found"}
	case errors.Is(err, ErrRepresentationNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "representation_not_found", Message: "representation not found"}
	default:
		return fmt.Errorf("%s: %w", action, err)
	}
}
