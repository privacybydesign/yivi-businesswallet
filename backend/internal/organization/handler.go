package organization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

type repository interface {
	List(ctx context.Context) ([]Organization, error)
	Create(ctx context.Context, name, slug string) (Organization, error)
	GetByID(ctx context.Context, id uuid.UUID) (Organization, error)
	GetBySlug(ctx context.Context, slug string) (Organization, error)
	Update(ctx context.Context, id uuid.UUID, name string) (Organization, error)
	ListForUser(ctx context.Context, userID uuid.UUID) ([]Organization, error)
	GetMembership(ctx context.Context, userID, orgID uuid.UUID) (Membership, error)
	ListMembers(ctx context.Context, orgID uuid.UUID) ([]Member, error)
	UpdateMembership(ctx context.Context, orgID, userID uuid.UUID, jobTitle *string, departmentID *uuid.UUID) (Member, error)
	ListDepartments(ctx context.Context, orgID uuid.UUID) ([]Department, error)
	CreateDepartment(ctx context.Context, orgID uuid.UUID, name string) (Department, error)
	UpdateDepartment(ctx context.Context, orgID, deptID uuid.UUID, name string) (Department, error)
	DeleteDepartment(ctx context.Context, orgID, deptID uuid.UUID) error
}

type inviter interface {
	InviteMember(ctx context.Context, orgID uuid.UUID, in Invite) (Member, error)
}

type Handler struct {
	store       repository
	service     inviter
	requireUser func(http.Handler) http.Handler
	admins      auth.PlatformAdmins
}

func NewHandler(store repository, service inviter, requireUser func(http.Handler) http.Handler, admins auth.PlatformAdmins) *Handler {
	return &Handler{store: store, service: service, requireUser: requireUser, admins: admins}
}

func (h *Handler) Register(mux *http.ServeMux) {
	platform := func(next http.Handler) http.Handler {
		return h.requireUser(auth.RequirePlatformAdmin(h.admins)(next))
	}
	orgScoped := func(next http.Handler) http.Handler {
		return h.requireUser(h.Authorize(next))
	}

	mux.Handle("GET /organizations", platform(respond.HandlerFunc(h.list)))
	mux.Handle("POST /organizations", platform(respond.HandlerFunc(h.create)))
	mux.Handle("GET /organizations/{id}", platform(respond.HandlerFunc(h.get)))

	mux.Handle("GET /me/organizations", h.requireUser(respond.HandlerFunc(h.listForUser)))

	mux.Handle("GET /orgs/{slug}", orgScoped(respond.HandlerFunc(h.details)))
	mux.Handle("PATCH /orgs/{slug}", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.update))))
	mux.Handle("GET /orgs/{slug}/members", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.members))))
	mux.Handle("POST /orgs/{slug}/members", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.addMember))))
	mux.Handle("PATCH /orgs/{slug}/members/{userId}", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.updateMember))))

	mux.Handle("GET /orgs/{slug}/departments", orgScoped(respond.HandlerFunc(h.listDepartments)))
	mux.Handle("POST /orgs/{slug}/departments", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.createDepartment))))
	mux.Handle("PATCH /orgs/{slug}/departments/{id}", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.updateDepartment))))
	mux.Handle("DELETE /orgs/{slug}/departments/{id}", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.deleteDepartment))))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	orgs, err := h.store.List(r.Context())
	if err != nil {
		return fmt.Errorf("listing organizations: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, orgs)
	return nil
}

type createRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	if req.Name == "" || req.Slug == "" {
		return badRequest("invalid_input", "name and slug are required")
	}
	switch err := ValidateSlug(req.Slug); {
	case errors.Is(err, ErrReservedSlug):
		return badRequest("reserved_slug", "slug is reserved and cannot be used")
	case errors.Is(err, ErrInvalidSlug):
		return badRequest("invalid_slug", "slug may only contain letters, numbers, and hyphens")
	}

	org, err := h.store.Create(r.Context(), req.Name, req.Slug)
	if errors.Is(err, ErrSlugTaken) {
		return &respond.APIError{Status: http.StatusConflict, Code: "slug_taken", Message: "slug already taken"}
	}
	if err != nil {
		return fmt.Errorf("creating organization: %w", err)
	}

	respond.JSON(w, r, http.StatusCreated, org)
	return nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid id")
	}

	org, err := h.store.GetByID(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		return &respond.APIError{Status: http.StatusNotFound, Code: "org_not_found", Message: "organization not found"}
	}
	if err != nil {
		return fmt.Errorf("getting organization %s: %w", id, err)
	}

	respond.JSON(w, r, http.StatusOK, org)
	return nil
}

func (h *Handler) listForUser(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFromContext(r.Context())
	orgs, err := h.store.ListForUser(r.Context(), u.ID)
	if err != nil {
		return fmt.Errorf("listing organizations for user: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, orgs)
	return nil
}

type orgDetailResponse struct {
	Organization
	Role string `json:"role"`
}

func (h *Handler) details(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	respond.JSON(w, r, http.StatusOK, orgDetailResponse{
		Organization: OrgFromContext(ctx),
		Role:         roleFromContext(ctx),
	})
	return nil
}

type updateRequest struct {
	Name string `json:"name"`
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return badRequest("invalid_input", "name is required")
	}

	org := OrgFromContext(r.Context())
	updated, err := h.store.Update(r.Context(), org.ID, req.Name)
	if errors.Is(err, ErrNotFound) {
		return &respond.APIError{Status: http.StatusNotFound, Code: "org_not_found", Message: "organization not found"}
	}
	if err != nil {
		return fmt.Errorf("updating organization: %w", err)
	}

	respond.JSON(w, r, http.StatusOK, updated)
	return nil
}

func badRequest(code, msg string) error {
	return &respond.APIError{Status: http.StatusBadRequest, Code: code, Message: msg}
}
