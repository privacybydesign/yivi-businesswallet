package verification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// maxBody caps a request body: a template is a handful of short strings.
const maxBody = 16 << 10

// templateStore is the CRUD the handler drives directly (no orchestration).
type templateStore interface {
	ListTemplates(ctx context.Context, orgID uuid.UUID) ([]Template, error)
	CreateTemplate(ctx context.Context, orgID uuid.UUID, in Template) (Template, error)
	DeleteTemplate(ctx context.Context, orgID, id uuid.UUID) error
}

// Handler serves the org-scoped verification API. Templates are managed by
// admins and readable by every member; running a check and reading its result
// is open to every member, since the fine-grained "verifications:run" permission
// of issue #245 needs the RBAC layer that is not in main (see
// .ai/conventions/BACKEND.md, "The RBAC and consent implementations").
type Handler struct {
	templates   templateStore
	svc         *Service
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(templates templateStore, svc *Service, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{templates: templates, svc: svc, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	member := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(next))
	}
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}

	mux.Handle("GET /orgs/{slug}/verifications/templates", member(respond.HandlerFunc(h.listTemplates)))
	mux.Handle("POST /orgs/{slug}/verifications/templates", admin(respond.HandlerFunc(h.createTemplate)))
	mux.Handle("DELETE /orgs/{slug}/verifications/templates/{id}", admin(respond.HandlerFunc(h.deleteTemplate)))

	mux.Handle("GET /orgs/{slug}/verifications", member(respond.HandlerFunc(h.list)))
	mux.Handle("POST /orgs/{slug}/verifications", member(respond.HandlerFunc(h.start)))
	mux.Handle("GET /orgs/{slug}/verifications/{id}", member(respond.HandlerFunc(h.get)))
}

type templateRequest struct {
	Name    string   `json:"name"`
	VCT     string   `json:"vct"`
	Claims  []string `json:"claims"`
	Purpose string   `json:"purpose"`
}

func (h *Handler) listTemplates(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	templates, err := h.templates.ListTemplates(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("listing verification templates: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, templates)
	return nil
}

func (h *Handler) createTemplate(w http.ResponseWriter, r *http.Request) error {
	req, err := decodeTemplate(w, r)
	if err != nil {
		return err
	}
	org := organization.OrgFromContext(r.Context())
	t, err := h.templates.CreateTemplate(r.Context(), org.ID, Template{
		Name: req.Name, VCT: req.VCT, Claims: req.Claims, Purpose: req.Purpose,
	})
	if err != nil {
		return fmt.Errorf("creating verification template: %w", err)
	}
	respond.JSON(w, r, http.StatusCreated, t)
	return nil
}

func (h *Handler) deleteTemplate(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return notFound("template_not_found", "template not found")
	}
	org := organization.OrgFromContext(r.Context())
	if err := h.templates.DeleteTemplate(r.Context(), org.ID, id); err != nil {
		return mapError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// decodeTemplate validates a template body: name and vct present, at least one
// non-empty claim, everything trimmed.
func decodeTemplate(w http.ResponseWriter, r *http.Request) (templateRequest, error) {
	var req templateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		return templateRequest{}, badRequest("invalid_body", "invalid request body")
	}
	req.Name = strings.TrimSpace(req.Name)
	req.VCT = strings.TrimSpace(req.VCT)
	req.Purpose = strings.TrimSpace(req.Purpose)
	if req.Name == "" {
		return templateRequest{}, badRequest("invalid_input", "name is required")
	}
	if req.VCT == "" {
		return templateRequest{}, badRequest("invalid_input", "vct is required")
	}
	claims := make([]string, 0, len(req.Claims))
	for _, c := range req.Claims {
		if c = strings.TrimSpace(c); c != "" {
			claims = append(claims, c)
		}
	}
	if len(claims) == 0 {
		return templateRequest{}, badRequest("invalid_input", "at least one claim is required")
	}
	req.Claims = claims
	return req, nil
}

type startRequest struct {
	TemplateID string `json:"templateId"`
}

func (h *Handler) start(w http.ResponseWriter, r *http.Request) error {
	var req startRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	templateID, err := uuid.Parse(req.TemplateID)
	if err != nil {
		return badRequest("invalid_input", "invalid templateId")
	}
	org := organization.OrgFromContext(r.Context())
	u := auth.UserFromContext(r.Context())
	v, err := h.svc.Start(r.Context(), org.ID, templateID, u.ID)
	if err != nil {
		if errors.Is(err, ErrVerifierUnavailable) {
			slog.ErrorContext(r.Context(), "verification start failed at verifier", slog.String("error", err.Error()))
		}
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusCreated, v)
	return nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return notFound("session_not_found", "verification not found")
	}
	org := organization.OrgFromContext(r.Context())
	v, err := h.svc.Get(r.Context(), org.ID, id)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, v)
	return nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	views, err := h.svc.List(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("listing verifications: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, views)
	return nil
}

// mapError turns the package sentinels into API errors; anything else is a 500
// logged by respond.HandlerFunc.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrTemplateNotFound):
		return notFound("template_not_found", "template not found")
	case errors.Is(err, ErrSessionNotFound):
		return notFound("session_not_found", "verification not found")
	case errors.Is(err, ErrVerifierUnavailable):
		return &respond.APIError{Status: http.StatusBadGateway, Code: "verifier_unavailable", Message: "the verifier could not start the request"}
	default:
		return fmt.Errorf("verification: %w", err)
	}
}

func badRequest(code, msg string) error {
	return &respond.APIError{Status: http.StatusBadRequest, Code: code, Message: msg}
}

func notFound(code, msg string) error {
	return &respond.APIError{Status: http.StatusNotFound, Code: code, Message: msg}
}
