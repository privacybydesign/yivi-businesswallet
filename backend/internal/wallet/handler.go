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
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// walletService is the surface the handler depends on.
type walletService interface {
	OpenWallet(ctx context.Context, requestorUserID uuid.UUID, kvkNumber string) (EnrollmentResult, error)
	StartRegisterSession(ctx context.Context) (auth.Session, error)
	Register(ctx context.Context, disclosureToken, kvkNumber string) (RegistrationOutcome, error)
	GetInstance(ctx context.Context, id uuid.UUID) (Instance, error)
	WalletForOrg(ctx context.Context, orgID uuid.UUID) (Instance, error)
	Representations(ctx context.Context, orgID uuid.UUID) ([]Representation, error)
	ClaimRepresentation(ctx context.Context, orgID, repID, userID uuid.UUID) error
	Suspend(ctx context.Context, orgID uuid.UUID) (Instance, error)
	Revoke(ctx context.Context, orgID uuid.UUID) (Instance, error)
}

// sessionIssuer logs the registrant in after a successful public registration:
// the identity disclosure already proved who they are.
type sessionIssuer interface {
	Issue(ctx context.Context, w http.ResponseWriter, userID uuid.UUID, idempotencyToken string) error
}

// Handler serves the public registration API (no session yet), the central
// "open a wallet" API for logged-in users, and the org-scoped wallet routes.
type Handler struct {
	service     walletService
	issuer      sessionIssuer
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(service walletService, issuer sessionIssuer, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{service: service, issuer: issuer, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	orgScoped := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(next))
	}
	orgAdmin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}

	// Public self-service registration (no account required yet): authenticate
	// via wallet disclosure, then bootstrap the wallet.
	mux.Handle("POST /register/session", respond.HandlerFunc(h.registerSession))
	mux.Handle("POST /register", respond.HandlerFunc(h.register))

	// Logged-in enrollment (uses the session user; no re-disclosure).
	mux.Handle("POST /wallet", h.requireUser(respond.HandlerFunc(h.open)))
	mux.Handle("GET /wallet/{id}", h.requireUser(respond.HandlerFunc(h.getInstance)))

	// Org-scoped.
	mux.Handle("GET /orgs/{slug}/wallet", orgScoped(respond.HandlerFunc(h.getForOrg)))
	mux.Handle("GET /orgs/{slug}/wallet/representations", orgScoped(respond.HandlerFunc(h.listRepresentations)))
	mux.Handle("POST /orgs/{slug}/wallet/representations/{id}/claim", orgScoped(respond.HandlerFunc(h.claim)))
	mux.Handle("POST /orgs/{slug}/wallet/suspend", orgAdmin(respond.HandlerFunc(h.suspend)))
	mux.Handle("POST /orgs/{slug}/wallet/revoke", orgAdmin(respond.HandlerFunc(h.revoke)))
}

type enrollResponse struct {
	Status                  string `json:"status"`
	OrganizationSlug        string `json:"organizationSlug"`
	LegalName               string `json:"legalName"`
	KVKNumber               string `json:"kvkNumber"`
	RepresentationKind      string `json:"representationKind,omitempty"`
	RepresentationAuthority string `json:"representationAuthority,omitempty"`
}

func toEnrollResponse(res EnrollmentResult) enrollResponse {
	return enrollResponse{
		Status:                  res.Instance.Status,
		OrganizationSlug:        res.Instance.OrganizationSlug,
		LegalName:               res.Instance.LegalName,
		KVKNumber:               res.Instance.KVKNumber,
		RepresentationKind:      res.RepresentationKind,
		RepresentationAuthority: res.RepresentationAuthority,
	}
}

func notRepresentative() error {
	return &respond.APIError{Status: http.StatusForbidden, Code: "not_a_representative", Message: "you are not registered as a representative of this company"}
}

func (h *Handler) registerSession(w http.ResponseWriter, r *http.Request) error {
	sess, err := h.service.StartRegisterSession(r.Context())
	if err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, sess)
	return nil
}

type registerRequest struct {
	DisclosureToken string `json:"disclosureToken"`
	KVKNumber       string `json:"kvkNumber"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) error {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	req.DisclosureToken = strings.TrimSpace(req.DisclosureToken)
	req.KVKNumber = strings.TrimSpace(req.KVKNumber)
	if req.DisclosureToken == "" {
		return badRequest("invalid_input", "disclosureToken is required")
	}
	if req.KVKNumber == "" {
		return badRequest("invalid_input", "kvkNumber is required")
	}

	outcome, err := h.service.Register(r.Context(), req.DisclosureToken, req.KVKNumber)
	if errors.Is(err, ErrRegistrationInProgress) {
		return &respond.APIError{Status: http.StatusConflict, Code: "registration_in_progress", Message: "a registration is already in progress for this company"}
	}
	if errors.Is(err, ErrAlreadyRegistered) {
		return &respond.APIError{Status: http.StatusConflict, Code: "already_registered", Message: "this company already has a business wallet"}
	}
	if err != nil {
		return mapError(err, "registering wallet")
	}
	if outcome.Result.Instance.Status == StatusRejected {
		return notRepresentative()
	}

	// The identity disclosure proved who they are; log them in.
	if err := h.issuer.Issue(r.Context(), w, outcome.UserID, req.DisclosureToken); err != nil {
		return fmt.Errorf("issuing session on register: %w", err)
	}
	respond.JSON(w, r, http.StatusCreated, toEnrollResponse(outcome.Result))
	return nil
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
	res, err := h.service.OpenWallet(r.Context(), u.ID, req.KVKNumber)
	if errors.Is(err, ErrRegistrationInProgress) {
		return &respond.APIError{Status: http.StatusConflict, Code: "registration_in_progress", Message: "a registration is already in progress for this company"}
	}
	if errors.Is(err, ErrAlreadyRegistered) {
		return &respond.APIError{Status: http.StatusConflict, Code: "already_registered", Message: "this company already has a business wallet"}
	}
	if err != nil {
		return mapError(err, "opening wallet")
	}
	if res.Instance.Status == StatusRejected {
		return notRepresentative()
	}

	respond.JSON(w, r, http.StatusCreated, toEnrollResponse(res))
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
