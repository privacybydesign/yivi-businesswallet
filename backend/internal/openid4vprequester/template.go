package openid4vprequester

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// A template is a saved credential request: a name and the credential types
// and claims to ask for, without a recipient. Pure CRUD, so the handler calls
// the store directly; the store checks a template with the same rules a send
// applies, so a picked template never fails as a request.

const (
	// maxTemplateName caps a template's name: it labels one option in a picker.
	maxTemplateName = 120
	// maxTemplateBody caps a create request: a name plus what a send carries.
	maxTemplateBody = maxSendBody
	// templateNameKey is the unique (organization_id, name) constraint.
	templateNameKey = "credential_request_templates_name_key"
	// uniqueViolation is Postgres' unique_violation SQLSTATE.
	uniqueViolation = "23505"
	// msgTemplateNotFound answers an unknown or malformed template id alike.
	msgTemplateNotFound = "unknown credential request template"
)

var (
	ErrTemplateNotFound  = errors.New("template_not_found")
	ErrTemplateNameTaken = errors.New("template_name_taken")
)

// Template is a saved credential request.
type Template struct {
	ID          uuid.UUID            `json:"id"`
	Name        string               `json:"name"`
	Credentials []TemplateCredential `json:"credentials"`
	CreatedAt   time.Time            `json:"createdAt"`
}

// TemplateCredential is one credential a template asks for. Unlike a
// CredentialRequest it has no query id: a send assigns those.
type TemplateCredential struct {
	VCT    string   `json:"vct"`
	Claims []string `json:"claims"`
}

// NewTemplate is what CreateTemplate persists.
type NewTemplate struct {
	OrganizationID uuid.UUID
	CreatedBy      *uuid.UUID
	Name           string
	Credentials    []CredentialRequest
}

const templateColumns = `id, name, credentials, created_at`

func scanTemplate(row pgx.Row) (Template, error) {
	var (
		t           Template
		credentials []byte
	)
	if err := row.Scan(&t.ID, &t.Name, &credentials, &t.CreatedAt); err != nil {
		return Template{}, err
	}
	if err := json.Unmarshal(credentials, &t.Credentials); err != nil {
		return Template{}, fmt.Errorf("openid4vprequester: decode template credentials: %w", err)
	}
	return t, nil
}

// normalizeTemplate trims the name and checks the credentials as a send would,
// keeping what they ask for without the query ids a send assigns.
func normalizeTemplate(in NewTemplate) (string, []TemplateCredential, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > maxTemplateName {
		return "", nil, fmt.Errorf("%w: a template needs a name of at most %d characters", ErrInvalidInput, maxTemplateName)
	}
	checked, err := normalizeCredentials(in.Credentials)
	if err != nil {
		return "", nil, err
	}
	creds := make([]TemplateCredential, 0, len(checked))
	for _, c := range checked {
		creds = append(creds, TemplateCredential{VCT: c.VCT, Claims: c.Claims})
	}
	return name, creds, nil
}

// CreateTemplate stores a template and audits it.
func (s *Store) CreateTemplate(ctx context.Context, in NewTemplate) (Template, error) {
	name, creds, err := normalizeTemplate(in)
	if err != nil {
		return Template{}, err
	}
	credentials, err := json.Marshal(creds)
	if err != nil {
		return Template{}, fmt.Errorf("openid4vprequester: encode template credentials: %w", err)
	}
	const q = `
		INSERT INTO credential_request_templates (organization_id, name, credentials, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING ` + templateColumns
	var t Template
	err = database.InTx(ctx, s.db, func(tx database.Querier) error {
		t, err = scanTemplate(tx.QueryRow(ctx, q, in.OrganizationID, name, credentials, in.CreatedBy))
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == templateNameKey {
			return ErrTemplateNameTaken
		}
		if err != nil {
			return fmt.Errorf("openid4vprequester: create template: %w", err)
		}
		return s.audit.Record(ctx, tx, audit.PresentationRequestTemplateCreated,
			audit.Target{Type: audit.TargetPresentationRequestTemplate, ID: t.ID.String(), OrgID: &in.OrganizationID},
			audit.Created(templateMetadata(t)))
	})
	if err != nil {
		return Template{}, err
	}
	return t, nil
}

// ListTemplates returns the organization's templates by name.
func (s *Store) ListTemplates(ctx context.Context, orgID uuid.UUID) ([]Template, error) {
	rows, err := s.db.Query(ctx, `SELECT `+templateColumns+`
		FROM credential_request_templates WHERE organization_id = $1 ORDER BY name`, orgID)
	if err != nil {
		return nil, fmt.Errorf("openid4vprequester: list templates org %s: %w", orgID, err)
	}
	defer rows.Close()
	out := []Template{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, fmt.Errorf("openid4vprequester: list templates scan: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("openid4vprequester: list templates rows org %s: %w", orgID, err)
	}
	return out, nil
}

// DeleteTemplate removes one of the organization's templates and audits it.
// Another organization's id is ErrTemplateNotFound, like an unknown one.
func (s *Store) DeleteTemplate(ctx context.Context, orgID, id uuid.UUID) error {
	const q = `DELETE FROM credential_request_templates WHERE organization_id = $1 AND id = $2
		RETURNING ` + templateColumns
	return database.InTx(ctx, s.db, func(tx database.Querier) error {
		t, err := scanTemplate(tx.QueryRow(ctx, q, orgID, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTemplateNotFound
		}
		if err != nil {
			return fmt.Errorf("openid4vprequester: delete template %s: %w", id, err)
		}
		return s.audit.Record(ctx, tx, audit.PresentationRequestTemplateDeleted,
			audit.Target{Type: audit.TargetPresentationRequestTemplate, ID: t.ID.String(), OrgID: &orgID},
			audit.Deleted(templateMetadata(t)))
	})
}

// templateMetadata is the audit snapshot: the name and the types asked for,
// like a sent request's.
func templateMetadata(t Template) map[string]any {
	types := make([]string, 0, len(t.Credentials))
	for _, c := range t.Credentials {
		types = append(types, c.VCT)
	}
	return map[string]any{"name": t.Name, "credentials": types}
}

// TemplateHandler serves an organization's templates to its admins.
type TemplateHandler struct {
	store       *Store
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewTemplateHandler(store *Store, requireUser, authorize func(http.Handler) http.Handler) *TemplateHandler {
	return &TemplateHandler{store: store, requireUser: requireUser, authorize: authorize}
}

func (h *TemplateHandler) Register(mux *http.ServeMux) {
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}
	mux.Handle("GET /orgs/{slug}/openid4vp/templates", admin(respond.HandlerFunc(h.list)))
	mux.Handle("POST /orgs/{slug}/openid4vp/templates", admin(respond.HandlerFunc(h.create)))
	mux.Handle("DELETE /orgs/{slug}/openid4vp/templates/{id}", admin(respond.HandlerFunc(h.delete)))
}

type createTemplateRequest struct {
	Name        string           `json:"name"`
	Credentials []sendCredential `json:"credentials"`
}

func (h *TemplateHandler) list(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	templates, err := h.store.ListTemplates(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("listing credential request templates: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, templates)
	return nil
}

func (h *TemplateHandler) create(w http.ResponseWriter, r *http.Request) error {
	var req createTemplateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTemplateBody)).Decode(&req); err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "invalid JSON body"}
	}
	creds := make([]CredentialRequest, 0, len(req.Credentials))
	for _, c := range req.Credentials {
		creds = append(creds, CredentialRequest{VCT: c.VCT, Claims: c.Claims})
	}
	u := auth.UserFromContext(r.Context())
	org := organization.OrgFromContext(r.Context())
	t, err := h.store.CreateTemplate(r.Context(), NewTemplate{
		OrganizationID: org.ID, CreatedBy: &u.ID, Name: req.Name, Credentials: creds,
	})
	if err != nil {
		return mapTemplateError(err)
	}
	respond.JSON(w, r, http.StatusCreated, t)
	return nil
}

func (h *TemplateHandler) delete(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return mapTemplateError(ErrTemplateNotFound)
	}
	org := organization.OrgFromContext(r.Context())
	if err := h.store.DeleteTemplate(r.Context(), org.ID, id); err != nil {
		return mapTemplateError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func mapTemplateError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidInput):
		return &respond.APIError{Status: http.StatusBadRequest, Code: ErrInvalidInput.Error(), Message: err.Error()}
	case errors.Is(err, ErrTemplateNameTaken):
		return &respond.APIError{Status: http.StatusConflict, Code: ErrTemplateNameTaken.Error(), Message: "a template with this name already exists"}
	case errors.Is(err, ErrTemplateNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: ErrTemplateNotFound.Error(), Message: msgTemplateNotFound}
	default:
		return fmt.Errorf("openid4vprequester: %w", err)
	}
}
