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
	OpenWallet(ctx context.Context, requestorUserID uuid.UUID, requester Requester, kvkNumber, slug string) (RegistrationResult, error)
	StartRegisterSession(ctx context.Context) (auth.Session, error)
	Register(ctx context.Context, disclosureToken, kvkNumber, slug string) (RegistrationOutcome, error)
	Representations(ctx context.Context, orgID uuid.UUID) ([]Representation, error)
	ClaimRepresentation(ctx context.Context, orgID, repID, userID uuid.UUID) error
	Suspend(ctx context.Context, orgID uuid.UUID) (organization.Organization, error)
	Revoke(ctx context.Context, orgID uuid.UUID) (organization.Organization, error)
}

// sessionIssuer logs the registrant in after a successful public registration.
type sessionIssuer interface {
	Issue(ctx context.Context, w http.ResponseWriter, userID uuid.UUID, idempotencyToken string) error
}

// Handler serves the public registration API (no session yet), the logged-in
// registration API, and the org-scoped wallet routes.
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
	// via wallet disclosure, then register the business wallet.
	mux.Handle("POST /register/session", respond.HandlerFunc(h.registerSession))
	mux.Handle("POST /register", respond.HandlerFunc(h.register))

	// Logged-in registration (register an additional company).
	mux.Handle("POST /wallet", h.requireUser(respond.HandlerFunc(h.open)))

	// Org-scoped.
	mux.Handle("GET /orgs/{slug}/wallet/representations", orgScoped(respond.HandlerFunc(h.listRepresentations)))
	mux.Handle("POST /orgs/{slug}/wallet/representations/{id}/claim", orgScoped(respond.HandlerFunc(h.claim)))
	mux.Handle("POST /orgs/{slug}/wallet/suspend", orgAdmin(respond.HandlerFunc(h.suspend)))
	mux.Handle("POST /orgs/{slug}/wallet/revoke", orgAdmin(respond.HandlerFunc(h.revoke)))
}

type enrollResponse struct {
	OrganizationSlug        string `json:"organizationSlug"`
	LegalName               string `json:"legalName"`
	KVKNumber               string `json:"kvkNumber"`
	Status                  string `json:"status"`
	RepresentationKind      string `json:"representationKind,omitempty"`
	RepresentationAuthority string `json:"representationAuthority,omitempty"`
}

func toEnrollResponse(res RegistrationResult) enrollResponse {
	return enrollResponse{
		OrganizationSlug:        res.Organization.Slug,
		LegalName:               res.Organization.Name,
		KVKNumber:               res.Organization.KVKNumber,
		Status:                  res.Organization.Status,
		RepresentationKind:      res.RepresentationKind,
		RepresentationAuthority: res.RepresentationAuthority,
	}
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
	Slug            string `json:"slug"`
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

	outcome, err := h.service.Register(r.Context(), req.DisclosureToken, req.KVKNumber, req.Slug)
	if e := mapError(err); e != nil {
		return e
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
	Slug      string `json:"slug"`
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
	// The logged-in path has no fresh disclosure, so KVK matches on the stored
	// name alone (no verified date of birth). See wallet.Requester.
	requester := Requester{GivenNames: u.GivenNames, FamilyName: u.LastName}
	res, err := h.service.OpenWallet(r.Context(), u.ID, requester, req.KVKNumber, req.Slug)
	if e := mapError(err); e != nil {
		return e
	}
	respond.JSON(w, r, http.StatusCreated, toEnrollResponse(res))
	return nil
}

func (h *Handler) listRepresentations(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	reps, err := h.service.Representations(r.Context(), org.ID)
	if e := mapError(err); e != nil {
		return e
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
	if e := mapError(h.service.ClaimRepresentation(r.Context(), org.ID, repID, u.ID)); e != nil {
		return e
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) suspend(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	updated, err := h.service.Suspend(r.Context(), org.ID)
	if e := mapError(err); e != nil {
		return e
	}
	respond.JSON(w, r, http.StatusOK, updated)
	return nil
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	updated, err := h.service.Revoke(r.Context(), org.ID)
	if e := mapError(err); e != nil {
		return e
	}
	respond.JSON(w, r, http.StatusOK, updated)
	return nil
}

func badRequest(code, msg string) error {
	return &respond.APIError{Status: http.StatusBadRequest, Code: code, Message: msg}
}

// mapError translates wallet/registration errors to API errors. It returns nil
// for a nil error, an *APIError for known cases, and a wrapped error otherwise
// (rendered as 500 by the HandlerFunc adapter).
func mapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotRepresentative):
		return &respond.APIError{Status: http.StatusForbidden, Code: "not_a_representative", Message: "you are not registered as a representative of this company"}
	case errors.Is(err, ErrUnknownKVK):
		return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "unknown_kvk", Message: "this KVK number is not in the register"}
	case errors.Is(err, ErrAlreadyRegistered):
		return &respond.APIError{Status: http.StatusConflict, Code: "already_registered", Message: "this company already has a business wallet"}
	case errors.Is(err, ErrSlugTaken):
		return &respond.APIError{Status: http.StatusConflict, Code: "slug_taken", Message: "slug already taken"}
	case errors.Is(err, organization.ErrReservedSlug):
		return badRequest("reserved_slug", "slug is reserved and cannot be used")
	case errors.Is(err, organization.ErrInvalidSlug):
		return badRequest("invalid_slug", "slug may only contain lowercase letters, numbers, and hyphens")
	case errors.Is(err, organization.ErrNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "wallet_not_found", Message: "wallet not found"}
	case errors.Is(err, ErrRepresentationNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "representation_not_found", Message: "representation not found"}
	case errors.Is(err, ErrNotImplemented):
		return &respond.APIError{Status: http.StatusNotImplemented, Code: "not_implemented", Message: "not implemented yet"}
	default:
		return fmt.Errorf("wallet: %w", err)
	}
}
