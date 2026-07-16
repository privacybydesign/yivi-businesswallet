package attestation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

type schemaStore interface {
	ListSchemas(ctx context.Context, orgID uuid.UUID) ([]Schema, error)
	GetSchema(ctx context.Context, orgID, id uuid.UUID) (Schema, error)
	CreateSchema(ctx context.Context, orgID uuid.UUID, in Schema) (Schema, error)
	UpdateSchema(ctx context.Context, orgID, id uuid.UUID, in Schema) (Schema, error)
	DeleteSchema(ctx context.Context, orgID, id uuid.UUID) error
}

type templateStore interface {
	ListTemplates(ctx context.Context, orgID uuid.UUID) ([]Template, error)
	GetTemplate(ctx context.Context, orgID, id uuid.UUID) (Template, error)
	CreateTemplate(ctx context.Context, orgID uuid.UUID, in Template) (Template, error)
	UpdateTemplate(ctx context.Context, orgID, id uuid.UUID, in Template) (Template, error)
	DeleteTemplate(ctx context.Context, orgID, id uuid.UUID) error
}

type keyStore interface {
	ListKeys(ctx context.Context, orgID uuid.UUID) ([]Key, error)
	CreateKey(ctx context.Context, orgID uuid.UUID, kind, label, providerRef string) (Key, error)
	SetKeyStatus(ctx context.Context, orgID, id uuid.UUID, status, action string) (Key, error)
}

type issuedReader interface {
	ListIssued(ctx context.Context, orgID uuid.UUID) ([]Issued, error)
}

type issuanceService interface {
	Issue(ctx context.Context, orgID, issuedBy uuid.UUID, orgName string, in IssueInput) (IssueResult, error)
	Status(ctx context.Context, orgID, id uuid.UUID) (Issued, error)
	Revoke(ctx context.Context, orgID, id uuid.UUID) (Issued, error)
	ClaimStatus(ctx context.Context, token string) (ClaimView, error)
}

// Handler serves the org-scoped attestations API (Schemas / Templates / Issued
// tabs + key material). Org routes compose the injected requireUser + authorize
// middleware; write/manage routes additionally require org admin.
type Handler struct {
	schemas     schemaStore
	templates   templateStore
	keys        keyStore
	issued      issuedReader
	service     issuanceService
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(schemas schemaStore, templates templateStore, keys keyStore, issued issuedReader, service issuanceService, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{
		schemas:     schemas,
		templates:   templates,
		keys:        keys,
		issued:      issued,
		service:     service,
		requireUser: requireUser,
		authorize:   authorize,
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	member := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(next))
	}
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}

	// Schemas (admin).
	mux.Handle("GET /orgs/{slug}/attestations/schemas", admin(respond.HandlerFunc(h.listSchemas)))
	mux.Handle("POST /orgs/{slug}/attestations/schemas", admin(respond.HandlerFunc(h.createSchema)))
	mux.Handle("GET /orgs/{slug}/attestations/schemas/{id}", admin(respond.HandlerFunc(h.getSchema)))
	mux.Handle("PATCH /orgs/{slug}/attestations/schemas/{id}", admin(respond.HandlerFunc(h.updateSchema)))
	mux.Handle("DELETE /orgs/{slug}/attestations/schemas/{id}", admin(respond.HandlerFunc(h.deleteSchema)))

	// Templates (admin).
	mux.Handle("GET /orgs/{slug}/attestations/templates", admin(respond.HandlerFunc(h.listTemplates)))
	mux.Handle("POST /orgs/{slug}/attestations/templates", admin(respond.HandlerFunc(h.createTemplate)))
	mux.Handle("GET /orgs/{slug}/attestations/templates/{id}", admin(respond.HandlerFunc(h.getTemplate)))
	mux.Handle("PATCH /orgs/{slug}/attestations/templates/{id}", admin(respond.HandlerFunc(h.updateTemplate)))
	mux.Handle("DELETE /orgs/{slug}/attestations/templates/{id}", admin(respond.HandlerFunc(h.deleteTemplate)))

	// Key material (admin).
	mux.Handle("GET /orgs/{slug}/attestations/keys", admin(respond.HandlerFunc(h.listKeys)))
	mux.Handle("POST /orgs/{slug}/attestations/keys", admin(respond.HandlerFunc(h.createKey)))
	mux.Handle("POST /orgs/{slug}/attestations/keys/{id}/suspend", admin(respond.HandlerFunc(h.suspendKey)))
	mux.Handle("POST /orgs/{slug}/attestations/keys/{id}/revoke", admin(respond.HandlerFunc(h.revokeKey)))

	// Issuance ledger (member read; admin issue/revoke).
	mux.Handle("GET /orgs/{slug}/attestations", member(respond.HandlerFunc(h.listIssued)))
	mux.Handle("POST /orgs/{slug}/attestations", admin(respond.HandlerFunc(h.issue)))
	mux.Handle("GET /orgs/{slug}/attestations/{id}", member(respond.HandlerFunc(h.getIssued)))
	mux.Handle("POST /orgs/{slug}/attestations/{id}/revoke", admin(respond.HandlerFunc(h.revoke)))

	// Public, unauthenticated claim view (keyed on an opaque claim token, never the
	// row id). The offer link e-mailed / QERDS-delivered to a recipient points here.
	mux.Handle("GET /attestations/claim/{token}", respond.HandlerFunc(h.claim))
}

func (h *Handler) claim(w http.ResponseWriter, r *http.Request) error {
	token := strings.TrimSpace(r.PathValue("token"))
	if token == "" {
		return badRequest("invalid_token", "invalid claim token")
	}
	view, err := h.service.ClaimStatus(r.Context(), token)
	if errors.Is(err, ErrClaimNotFound) {
		return notFound("claim_not_found", "claim not found")
	}
	if err != nil {
		return fmt.Errorf("resolving claim: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, view)
	return nil
}

// --- Schemas ---

type schemaRequest struct {
	VCT                string         `json:"vct"`
	DisplayName        string         `json:"displayName"`
	CredentialConfigID string         `json:"credentialConfigId"`
	SubjectType        string         `json:"subjectType"`
	Attributes         []AttributeDef `json:"attributes"`
	Qualified          bool           `json:"qualified"`
	Status             string         `json:"status"`
}

func (h *Handler) listSchemas(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	schemas, err := h.schemas.ListSchemas(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("listing attestation schemas: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, schemas)
	return nil
}

func (h *Handler) getSchema(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r, "id", "schema")
	if err != nil {
		return err
	}
	org := organization.OrgFromContext(r.Context())
	sc, err := h.schemas.GetSchema(r.Context(), org.ID, id)
	if errors.Is(err, ErrSchemaNotFound) {
		return notFound("schema_not_found", "schema not found")
	}
	if err != nil {
		return fmt.Errorf("getting attestation schema: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, sc)
	return nil
}

func (h *Handler) createSchema(w http.ResponseWriter, r *http.Request) error {
	req, err := decodeSchema(r)
	if err != nil {
		return err
	}
	org := organization.OrgFromContext(r.Context())
	sc, err := h.schemas.CreateSchema(r.Context(), org.ID, Schema{
		VCT: req.VCT, DisplayName: req.DisplayName, CredentialConfigID: req.CredentialConfigID,
		SubjectType: req.SubjectType, Attributes: req.Attributes, Qualified: req.Qualified, Status: req.Status,
	})
	if errors.Is(err, ErrSchemaVctTaken) {
		return &respond.APIError{Status: http.StatusConflict, Code: "vct_taken", Message: "a schema with that vct already exists"}
	}
	if err != nil {
		return fmt.Errorf("creating attestation schema: %w", err)
	}
	respond.JSON(w, r, http.StatusCreated, sc)
	return nil
}

func (h *Handler) updateSchema(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r, "id", "schema")
	if err != nil {
		return err
	}
	req, err := decodeSchema(r)
	if err != nil {
		return err
	}
	org := organization.OrgFromContext(r.Context())
	sc, err := h.schemas.UpdateSchema(r.Context(), org.ID, id, Schema{
		DisplayName: req.DisplayName, CredentialConfigID: req.CredentialConfigID,
		SubjectType: req.SubjectType, Attributes: req.Attributes, Qualified: req.Qualified, Status: req.Status,
	})
	if errors.Is(err, ErrSchemaNotFound) {
		return notFound("schema_not_found", "schema not found")
	}
	if err != nil {
		return fmt.Errorf("updating attestation schema: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, sc)
	return nil
}

func (h *Handler) deleteSchema(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r, "id", "schema")
	if err != nil {
		return err
	}
	org := organization.OrgFromContext(r.Context())
	if err := h.schemas.DeleteSchema(r.Context(), org.ID, id); errors.Is(err, ErrSchemaNotFound) {
		return notFound("schema_not_found", "schema not found")
	} else if err != nil {
		return fmt.Errorf("deleting attestation schema: %w", err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func decodeSchema(r *http.Request) (schemaRequest, error) {
	var req schemaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return schemaRequest{}, badRequest("invalid_body", "invalid request body")
	}
	req.VCT = strings.TrimSpace(req.VCT)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.CredentialConfigID = strings.TrimSpace(req.CredentialConfigID)
	if req.DisplayName == "" {
		return schemaRequest{}, badRequest("invalid_input", "displayName is required")
	}
	if req.CredentialConfigID == "" {
		return schemaRequest{}, badRequest("invalid_input", "credentialConfigId is required")
	}
	if req.Status != "" && req.Status != SchemaDraft && req.Status != SchemaActive && req.Status != SchemaDeprecated {
		return schemaRequest{}, badRequest("invalid_input", "invalid status")
	}
	if req.SubjectType != "" && req.SubjectType != SubjectNaturalPerson && req.SubjectType != SubjectOrganization {
		return schemaRequest{}, badRequest("invalid_input", "invalid subjectType")
	}
	return req, nil
}

// --- Templates ---

type templateRequest struct {
	SchemaID          string            `json:"schemaId"`
	Name              string            `json:"name"`
	DefaultAttributes map[string]string `json:"defaultAttributes"`
	ValiditySeconds   *int              `json:"validitySeconds"`
	KeyMaterialID     *string           `json:"keyMaterialId"`
	Status            string            `json:"status"`
}

func (h *Handler) listTemplates(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	templates, err := h.templates.ListTemplates(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("listing attestation templates: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, templates)
	return nil
}

func (h *Handler) getTemplate(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r, "id", "template")
	if err != nil {
		return err
	}
	org := organization.OrgFromContext(r.Context())
	t, err := h.templates.GetTemplate(r.Context(), org.ID, id)
	if errors.Is(err, ErrTemplateNotFound) {
		return notFound("template_not_found", "template not found")
	}
	if err != nil {
		return fmt.Errorf("getting attestation template: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, t)
	return nil
}

func (h *Handler) createTemplate(w http.ResponseWriter, r *http.Request) error {
	req, err := decodeTemplate(r)
	if err != nil {
		return err
	}
	schemaID, err := uuid.Parse(req.SchemaID)
	if err != nil {
		return badRequest("invalid_input", "invalid schemaId")
	}
	keyID, err := parseOptionalUUID(req.KeyMaterialID)
	if err != nil {
		return badRequest("invalid_input", "invalid keyMaterialId")
	}

	org := organization.OrgFromContext(r.Context())
	t, err := h.templates.CreateTemplate(r.Context(), org.ID, Template{
		SchemaID: schemaID, Name: req.Name, DefaultAttributes: req.DefaultAttributes,
		ValiditySeconds: req.ValiditySeconds, KeyMaterialID: keyID,
	})
	if errors.Is(err, ErrSchemaNotFound) {
		return badRequest("invalid_input", "schema not found")
	}
	if err != nil {
		return fmt.Errorf("creating attestation template: %w", err)
	}
	respond.JSON(w, r, http.StatusCreated, t)
	return nil
}

func (h *Handler) updateTemplate(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r, "id", "template")
	if err != nil {
		return err
	}
	req, err := decodeTemplate(r)
	if err != nil {
		return err
	}
	keyID, err := parseOptionalUUID(req.KeyMaterialID)
	if err != nil {
		return badRequest("invalid_input", "invalid keyMaterialId")
	}

	org := organization.OrgFromContext(r.Context())
	t, err := h.templates.UpdateTemplate(r.Context(), org.ID, id, Template{
		Name: req.Name, DefaultAttributes: req.DefaultAttributes,
		ValiditySeconds: req.ValiditySeconds, KeyMaterialID: keyID, Status: req.Status,
	})
	if errors.Is(err, ErrTemplateNotFound) {
		return notFound("template_not_found", "template not found")
	}
	if err != nil {
		return fmt.Errorf("updating attestation template: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, t)
	return nil
}

func (h *Handler) deleteTemplate(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r, "id", "template")
	if err != nil {
		return err
	}
	org := organization.OrgFromContext(r.Context())
	if err := h.templates.DeleteTemplate(r.Context(), org.ID, id); errors.Is(err, ErrTemplateNotFound) {
		return notFound("template_not_found", "template not found")
	} else if err != nil {
		return fmt.Errorf("deleting attestation template: %w", err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func decodeTemplate(r *http.Request) (templateRequest, error) {
	var req templateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return templateRequest{}, badRequest("invalid_body", "invalid request body")
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return templateRequest{}, badRequest("invalid_input", "name is required")
	}
	if req.Status != "" && req.Status != TemplateActive && req.Status != TemplateArchived {
		return templateRequest{}, badRequest("invalid_input", "invalid status")
	}
	return req, nil
}

// --- Key material ---

type keyRequest struct {
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	ProviderRef string `json:"providerRef"`
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	keys, err := h.keys.ListKeys(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("listing attestation keys: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, keys)
	return nil
}

func (h *Handler) createKey(w http.ResponseWriter, r *http.Request) error {
	var req keyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	req.Kind = strings.TrimSpace(req.Kind)
	req.Label = strings.TrimSpace(req.Label)
	if req.Kind != KeyWalletManaged && req.Kind != KeyQualifiedCertificate {
		return badRequest("invalid_input", "invalid kind")
	}
	if req.Label == "" {
		return badRequest("invalid_input", "label is required")
	}

	org := organization.OrgFromContext(r.Context())
	key, err := h.keys.CreateKey(r.Context(), org.ID, req.Kind, req.Label, strings.TrimSpace(req.ProviderRef))
	if err != nil {
		return fmt.Errorf("creating attestation key: %w", err)
	}
	respond.JSON(w, r, http.StatusCreated, key)
	return nil
}

func (h *Handler) suspendKey(w http.ResponseWriter, r *http.Request) error {
	return h.setKeyStatus(w, r, KeySuspended, audit.AttestationKeySuspended)
}

func (h *Handler) revokeKey(w http.ResponseWriter, r *http.Request) error {
	return h.setKeyStatus(w, r, KeyRevoked, audit.AttestationKeyRevoked)
}

func (h *Handler) setKeyStatus(w http.ResponseWriter, r *http.Request, status, action string) error {
	id, err := parseID(r, "id", "key")
	if err != nil {
		return err
	}
	org := organization.OrgFromContext(r.Context())
	key, err := h.keys.SetKeyStatus(r.Context(), org.ID, id, status, action)
	if errors.Is(err, ErrKeyNotFound) {
		return notFound("key_not_found", "key not found")
	}
	if err != nil {
		return fmt.Errorf("updating attestation key: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, key)
	return nil
}

// --- Issuance ledger ---

type recipientRequest struct {
	Kind   string  `json:"kind"`
	UserID *string `json:"userId"`
	Ref    string  `json:"ref"`
}

type issueRequest struct {
	TemplateID string            `json:"templateId"`
	Recipient  recipientRequest  `json:"recipient"`
	Attributes map[string]string `json:"attributes"`
}

func (h *Handler) listIssued(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	issued, err := h.issued.ListIssued(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("listing issued attestations: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, issued)
	return nil
}

func (h *Handler) getIssued(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r, "id", "attestation")
	if err != nil {
		return err
	}
	org := organization.OrgFromContext(r.Context())
	issued, err := h.service.Status(r.Context(), org.ID, id)
	if errors.Is(err, ErrIssuedNotFound) {
		return notFound("attestation_not_found", "attestation not found")
	}
	if err != nil {
		return fmt.Errorf("getting issued attestation: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, issued)
	return nil
}

func (h *Handler) issue(w http.ResponseWriter, r *http.Request) error {
	var req issueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	templateID, err := uuid.Parse(strings.TrimSpace(req.TemplateID))
	if err != nil {
		return badRequest("invalid_input", "invalid templateId")
	}
	if req.Recipient.Kind != RecipientMember && req.Recipient.Kind != RecipientExternal && req.Recipient.Kind != RecipientOrganization {
		return badRequest("invalid_input", "invalid recipient kind")
	}
	ref := strings.TrimSpace(req.Recipient.Ref)
	if ref == "" {
		return badRequest("invalid_input", "recipient ref is required")
	}
	userID, err := parseOptionalUUID(req.Recipient.UserID)
	if err != nil {
		return badRequest("invalid_input", "invalid recipient userId")
	}
	if req.Attributes == nil {
		req.Attributes = map[string]string{}
	}

	org := organization.OrgFromContext(r.Context())
	actor := auth.UserFromContext(r.Context())
	result, err := h.service.Issue(r.Context(), org.ID, actor.ID, org.Name, IssueInput{
		TemplateID: templateID,
		Recipient:  Recipient{Kind: req.Recipient.Kind, UserID: userID, Ref: ref},
		Attributes: req.Attributes,
	})
	switch {
	case errors.Is(err, ErrTemplateNotFound):
		return notFound("template_not_found", "template not found")
	case errors.Is(err, ErrRecipientKindMismatch):
		return badRequest("recipient_kind_mismatch", "recipient kind does not match the schema subject type")
	case errors.Is(err, ErrUnknownAttribute):
		return badRequest("unknown_attribute", err.Error())
	case errors.Is(err, ErrMissingAttribute):
		return badRequest("missing_attribute", err.Error())
	case err != nil:
		return fmt.Errorf("issuing attestation: %w", err)
	}
	respond.JSON(w, r, http.StatusAccepted, result)
	return nil
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r, "id", "attestation")
	if err != nil {
		return err
	}
	org := organization.OrgFromContext(r.Context())
	issued, err := h.service.Revoke(r.Context(), org.ID, id)
	switch {
	case errors.Is(err, ErrIssuedNotFound):
		return notFound("attestation_not_found", "attestation not found")
	case errors.Is(err, ErrNotOfferable):
		return &respond.APIError{Status: http.StatusConflict, Code: "not_revocable", Message: "attestation is not in a revocable state"}
	case err != nil:
		return fmt.Errorf("revoking attestation: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, issued)
	return nil
}

// --- helpers ---

func parseID(r *http.Request, param, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue(param))
	if err != nil {
		return uuid.Nil, badRequest("invalid_id", "invalid "+name+" id")
	}
	return id, nil
}

func parseOptionalUUID(v *string) (*uuid.UUID, error) {
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil, nil
	}
	id, err := uuid.Parse(strings.TrimSpace(*v))
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func badRequest(code, msg string) error {
	return &respond.APIError{Status: http.StatusBadRequest, Code: code, Message: msg}
}

func notFound(code, msg string) error {
	return &respond.APIError{Status: http.StatusNotFound, Code: code, Message: msg}
}
