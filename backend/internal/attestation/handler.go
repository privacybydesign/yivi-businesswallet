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
	GetSchemaLogo(ctx context.Context, orgID, id uuid.UUID) (Logo, error)
	SetSchemaLogo(ctx context.Context, orgID, id uuid.UUID, logo LogoUpdate) (Schema, error)
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

// onboardingStore reads and replaces an org's onboarding auto-issue set (the
// templates issued to a new member on accept). Implemented by the attestation Store.
type onboardingStore interface {
	ListOnboardingAttestations(ctx context.Context, orgID uuid.UUID) ([]OnboardingAttestation, error)
	SetOnboardingAttestations(ctx context.Context, orgID uuid.UUID, templateIDs []uuid.UUID) ([]OnboardingAttestation, error)
}

type issuanceService interface {
	Issue(ctx context.Context, orgID uuid.UUID, issuedBy *uuid.UUID, orgName string, in IssueInput) (IssueResult, error)
	Status(ctx context.Context, orgID, id uuid.UUID) (Issued, error)
	Revoke(ctx context.Context, orgID, id uuid.UUID) (Issued, error)
	Cancel(ctx context.Context, orgID, id uuid.UUID) (Issued, error)
	ClaimStatus(ctx context.Context, token string) (ClaimView, error)
	DeleteHeld(ctx context.Context, orgID, id uuid.UUID) error
	ListHeld(ctx context.Context, orgID uuid.UUID, lang string) ([]HeldListView, error)
	HeldClaims(ctx context.Context, orgID, id uuid.UUID, lang string) (HeldClaimsView, error)
}

// issuerSettingsReader resolves an org's issuer instance name (defaulted to the
// fallback when unconfigured) and branding, for generating the issuer bundle.
// Implemented by internal/issuersettings.Store.
type issuerSettingsReader interface {
	BundleConfig(ctx context.Context, orgID uuid.UUID, fallbackInstance string) (instance, displayName, logoURI string, err error)
}

// Handler serves the org-scoped attestations API (Schemas / Templates / Issued
// tabs + key material). Org routes compose the injected requireUser + authorize
// middleware; write/manage routes additionally require org admin.
//
// The held-credential list read runs through the service (it enriches each row
// with the credential's localized display metadata from the holder engine, §6.5),
// as does deletion (it also removes the credential from the engine).
type Handler struct {
	schemas        schemaStore
	templates      templateStore
	keys           keyStore
	issued         issuedReader
	service        issuanceService
	issuerSettings issuerSettingsReader
	onboarding     onboardingStore
	issuerURL      string
	requireUser    func(http.Handler) http.Handler
	authorize      func(http.Handler) http.Handler
}

// NewHandler wires the attestations API. issuerURL is the hosted issuer instance
// base URL, emitted into generated VCT documents (see schemaIssuerConfig); it may
// be empty (the generated config's issuer field is then left for the operator).
// issuerSettings resolves an org's issuer instance + branding for the per-org
// bundle generator (see issuerBundle).
func NewHandler(schemas schemaStore, templates templateStore, keys keyStore, issued issuedReader, service issuanceService, issuerSettings issuerSettingsReader, onboarding onboardingStore, issuerURL string, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{
		schemas:        schemas,
		templates:      templates,
		keys:           keys,
		issued:         issued,
		service:        service,
		issuerSettings: issuerSettings,
		onboarding:     onboarding,
		issuerURL:      issuerURL,
		requireUser:    requireUser,
		authorize:      authorize,
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	member := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(next))
	}
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}
	// Issuing, cancelling an offer and revoking are the attestation-issuer
	// surface: an admin holds all of attestations:*, an attestation_issuer holds
	// exactly these three. Schema/template/key management stays admin-only
	// (manage_templates / manage_keys), which an attestation_issuer does not hold.
	perm := func(action string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return h.requireUser(h.authorize(organization.RequirePermission(organization.ResourceAttestations, action)(next)))
		}
	}
	issue := perm(organization.ActionIssue)
	cancelOffer := perm(organization.ActionCancelOffer)
	revoke := perm(organization.ActionRevoke)

	// Schemas (admin).
	mux.Handle("GET /orgs/{slug}/attestations/schemas", admin(respond.HandlerFunc(h.listSchemas)))
	mux.Handle("POST /orgs/{slug}/attestations/schemas", admin(respond.HandlerFunc(h.createSchema)))
	mux.Handle("GET /orgs/{slug}/attestations/schemas/{id}", admin(respond.HandlerFunc(h.getSchema)))
	mux.Handle("GET /orgs/{slug}/attestations/schemas/{id}/issuer-config", admin(respond.HandlerFunc(h.schemaIssuerConfig)))
	mux.Handle("GET /orgs/{slug}/attestations/schemas/{id}/logo", admin(respond.HandlerFunc(h.serveSchemaLogo)))
	mux.Handle("PUT /orgs/{slug}/attestations/schemas/{id}/logo", admin(respond.HandlerFunc(h.putSchemaLogo)))
	mux.Handle("PATCH /orgs/{slug}/attestations/schemas/{id}", admin(respond.HandlerFunc(h.updateSchema)))
	mux.Handle("DELETE /orgs/{slug}/attestations/schemas/{id}", admin(respond.HandlerFunc(h.deleteSchema)))

	// Per-org issuer GitOps bundle generator (admin).
	mux.Handle("GET /orgs/{slug}/attestations/issuer-bundle", admin(respond.HandlerFunc(h.issuerBundle)))

	// Templates (admin).
	mux.Handle("GET /orgs/{slug}/attestations/templates", admin(respond.HandlerFunc(h.listTemplates)))
	mux.Handle("POST /orgs/{slug}/attestations/templates", admin(respond.HandlerFunc(h.createTemplate)))
	mux.Handle("GET /orgs/{slug}/attestations/templates/{id}", admin(respond.HandlerFunc(h.getTemplate)))
	mux.Handle("PATCH /orgs/{slug}/attestations/templates/{id}", admin(respond.HandlerFunc(h.updateTemplate)))
	mux.Handle("DELETE /orgs/{slug}/attestations/templates/{id}", admin(respond.HandlerFunc(h.deleteTemplate)))

	// Onboarding auto-issue set (admin): the templates issued to a new member on accept.
	mux.Handle("GET /orgs/{slug}/attestations/onboarding", admin(respond.HandlerFunc(h.listOnboarding)))
	mux.Handle("PUT /orgs/{slug}/attestations/onboarding", admin(respond.HandlerFunc(h.putOnboarding)))

	// Key material (admin).
	mux.Handle("GET /orgs/{slug}/attestations/keys", admin(respond.HandlerFunc(h.listKeys)))
	mux.Handle("POST /orgs/{slug}/attestations/keys", admin(respond.HandlerFunc(h.createKey)))
	mux.Handle("POST /orgs/{slug}/attestations/keys/{id}/suspend", admin(respond.HandlerFunc(h.suspendKey)))
	mux.Handle("POST /orgs/{slug}/attestations/keys/{id}/revoke", admin(respond.HandlerFunc(h.revokeKey)))

	// Issuance ledger (member read; admin issue/cancel/revoke).
	mux.Handle("GET /orgs/{slug}/attestations", member(respond.HandlerFunc(h.listIssued)))
	mux.Handle("POST /orgs/{slug}/attestations", issue(respond.HandlerFunc(h.issue)))
	mux.Handle("GET /orgs/{slug}/attestations/{id}", member(respond.HandlerFunc(h.getIssued)))
	mux.Handle("POST /orgs/{slug}/attestations/{id}/cancel", cancelOffer(respond.HandlerFunc(h.cancel)))
	mux.Handle("POST /orgs/{slug}/attestations/{id}/revoke", revoke(respond.HandlerFunc(h.revoke)))

	// Held credentials (member read; admin delete). Art 5(1)(a) "store, select".
	mux.Handle("GET /orgs/{slug}/attestations/held", member(respond.HandlerFunc(h.listHeld)))
	mux.Handle("GET /orgs/{slug}/attestations/held/{id}/claims", member(respond.HandlerFunc(h.heldClaims)))
	mux.Handle("DELETE /orgs/{slug}/attestations/held/{id}", admin(respond.HandlerFunc(h.deleteHeld)))

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

// --- Held credentials ---

// defaultLanguage is the language held-credential display metadata is resolved in
// when the request names none — matching the frontend's default and the "en"
// preference the engine already fell back to before localization.
const defaultLanguage = "en"

// requestLanguage reads the active app language from the `lang` query parameter
// (a BCP-47 tag such as "en" or "nl", sent by the frontend from i18next), so the
// held-credential title, labels and issuer name follow the user's language. It
// falls back to defaultLanguage when the parameter is absent or blank; the engine
// resolves an unknown tag to its English/first entry, so no allow-list is needed.
func requestLanguage(r *http.Request) string {
	if lang := strings.TrimSpace(r.URL.Query().Get("lang")); lang != "" {
		return lang
	}
	return defaultLanguage
}

func (h *Handler) listHeld(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	held, err := h.service.ListHeld(r.Context(), org.ID, requestLanguage(r))
	if err != nil {
		return fmt.Errorf("listing held attestations: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, held)
	return nil
}

func (h *Handler) heldClaims(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid held attestation id")
	}
	org := organization.OrgFromContext(r.Context())
	view, err := h.service.HeldClaims(r.Context(), org.ID, id, requestLanguage(r))
	switch {
	case errors.Is(err, ErrHeldNotFound):
		return notFound("held_not_found", "held attestation not found")
	case err != nil:
		return fmt.Errorf("reading held attestation claims: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, view)
	return nil
}

func (h *Handler) deleteHeld(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid held attestation id")
	}
	org := organization.OrgFromContext(r.Context())
	switch err := h.service.DeleteHeld(r.Context(), org.ID, id); {
	case errors.Is(err, ErrHeldNotFound):
		return notFound("held_not_found", "held attestation not found")
	case err != nil:
		return fmt.Errorf("deleting held attestation: %w", err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// --- Schemas ---

type schemaRequest struct {
	VCT                string          `json:"vct"`
	DisplayName        string          `json:"displayName"`
	CredentialConfigID string          `json:"credentialConfigId"`
	SubjectType        string          `json:"subjectType"`
	Attributes         []AttributeDef  `json:"attributes"`
	Display            []LocalizedName `json:"display"`
	Qualified          bool            `json:"qualified"`
	Status             string          `json:"status"`
}

func (h *Handler) listSchemas(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	schemas, err := h.schemas.ListSchemas(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("listing attestation schemas: %w", err)
	}
	for i := range schemas {
		schemas[i].LogoURI = schemaLogoURL(org.Slug, schemas[i])
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
	sc.LogoURI = schemaLogoURL(org.Slug, sc)
	respond.JSON(w, r, http.StatusOK, sc)
	return nil
}

// schemaIssuerConfig returns the Veramo issuer GitOps config generated from a
// schema's display metadata: the credential_configurations_supported fragment an
// operator merges into conf/metadata/<instance>.json plus a matching VCT
// document. This is how a schema's translations reach the credential — the hosted
// issuer's runtime config API is disabled in the deployment, so display is
// provisioned by files (openid4vc-poc-ops). See internal/attestation/issuerconfig.go.
func (h *Handler) schemaIssuerConfig(w http.ResponseWriter, r *http.Request) error {
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
	logoURI, err := h.schemaLogoDataURI(r.Context(), org.ID, sc)
	if err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, BuildIssuerConfig(sc, h.issuerURL, logoURI))
	return nil
}

// schemaLogoDataURI resolves a schema's stored image as a data: URI for embedding
// in the generated issuer config, or "" when the schema has no image.
func (h *Handler) schemaLogoDataURI(ctx context.Context, orgID uuid.UUID, sc Schema) (string, error) {
	if !sc.HasLogo {
		return "", nil
	}
	logo, err := h.schemas.GetSchemaLogo(ctx, orgID, sc.ID)
	if errors.Is(err, ErrNoSchemaLogo) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("getting attestation schema logo: %w", err)
	}
	return logoDataURI(logo), nil
}

// issuerBundle returns the full Veramo issuer GitOps bundle for the org: the
// issuer registration, its did:web key, the issuer metadata (carrying every
// schema's localized display), and one VCT document per schema — for an operator
// to commit to openid4vc-poc-ops and redeploy. The instance name defaults to the
// org slug when the org has not configured one.
func (h *Handler) issuerBundle(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	instance, displayName, logoURI, err := h.issuerSettings.BundleConfig(r.Context(), org.ID, org.Slug)
	if err != nil {
		return fmt.Errorf("resolving issuer settings: %w", err)
	}
	schemas, err := h.schemas.ListSchemas(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("listing attestation schemas: %w", err)
	}
	schemaLogos := make(map[uuid.UUID]string)
	for _, sc := range schemas {
		dataURI, err := h.schemaLogoDataURI(r.Context(), org.ID, sc)
		if err != nil {
			return err
		}
		if dataURI != "" {
			schemaLogos[sc.ID] = dataURI
		}
	}
	respond.JSON(w, r, http.StatusOK, BuildIssuerBundle(instance, displayName, logoURI, schemas, schemaLogos))
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
		SubjectType: req.SubjectType, Attributes: req.Attributes, Display: req.Display, Qualified: req.Qualified, Status: req.Status,
	})
	if errors.Is(err, ErrSchemaVctTaken) {
		return &respond.APIError{Status: http.StatusConflict, Code: "vct_taken", Message: "a schema with that vct already exists"}
	}
	if err != nil {
		return fmt.Errorf("creating attestation schema: %w", err)
	}
	sc.LogoURI = schemaLogoURL(org.Slug, sc)
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
		SubjectType: req.SubjectType, Attributes: req.Attributes, Display: req.Display, Qualified: req.Qualified, Status: req.Status,
	})
	if errors.Is(err, ErrSchemaNotFound) {
		return notFound("schema_not_found", "schema not found")
	}
	if err != nil {
		return fmt.Errorf("updating attestation schema: %w", err)
	}
	sc.LogoURI = schemaLogoURL(org.Slug, sc)
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
	attrs, err := normalizeAttributes(req.Attributes)
	if err != nil {
		return schemaRequest{}, err
	}
	req.Attributes = attrs
	display, err := normalizeNames(req.Display)
	if err != nil {
		return schemaRequest{}, err
	}
	req.Display = display
	return req, nil
}

// normalizeAttributes trims and validates a schema's attribute list: every type
// must be a supported claim type, and every localized label must carry both a
// language tag and text with no duplicate languages. A missing type defaults to
// string; fully-empty localized entries are dropped.
func normalizeAttributes(attrs []AttributeDef) ([]AttributeDef, error) {
	out := make([]AttributeDef, 0, len(attrs))
	for _, a := range attrs {
		a.Key = strings.TrimSpace(a.Key)
		a.Label = strings.TrimSpace(a.Label)
		a.Type = strings.TrimSpace(a.Type)
		if a.Type == "" {
			a.Type = AttributeTypeString
		}
		if !isSupportedAttributeType(a.Type) {
			return nil, badRequest("invalid_input", "unsupported attribute type: "+a.Type)
		}
		labels, err := normalizeLabels(a.Display)
		if err != nil {
			return nil, err
		}
		a.Display = labels
		out = append(out, a)
	}
	return out, nil
}

func normalizeNames(in []LocalizedName) ([]LocalizedName, error) {
	out := make([]LocalizedName, 0, len(in))
	pairs := make([][2]string, 0, len(in))
	for _, d := range in {
		d.Lang = strings.TrimSpace(d.Lang)
		d.Name = strings.TrimSpace(d.Name)
		if d.Lang == "" && d.Name == "" {
			continue
		}
		out = append(out, d)
		pairs = append(pairs, [2]string{d.Lang, d.Name})
	}
	if err := validateLocaleEntries(pairs); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeLabels(in []LocalizedLabel) ([]LocalizedLabel, error) {
	out := make([]LocalizedLabel, 0, len(in))
	pairs := make([][2]string, 0, len(in))
	for _, d := range in {
		d.Lang = strings.TrimSpace(d.Lang)
		d.Label = strings.TrimSpace(d.Label)
		if d.Lang == "" && d.Label == "" {
			continue
		}
		out = append(out, d)
		pairs = append(pairs, [2]string{d.Lang, d.Label})
	}
	if err := validateLocaleEntries(pairs); err != nil {
		return nil, err
	}
	return out, nil
}

// validateLocaleEntries checks (lang, text) display entries: each needs a
// non-empty language tag and text, with no repeated language.
func validateLocaleEntries(entries [][2]string) error {
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		lang, text := e[0], e[1]
		if lang == "" || text == "" {
			return badRequest("invalid_input", "each translation needs a language and text")
		}
		if seen[lang] {
			return badRequest("invalid_input", "duplicate translation language: "+lang)
		}
		seen[lang] = true
	}
	return nil
}

// --- Templates ---

type templateRequest struct {
	SchemaID          string            `json:"schemaId"`
	Name              string            `json:"name"`
	DefaultAttributes map[string]string `json:"defaultAttributes"`
	AttributeSources  map[string]string `json:"attributeSources"`
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

type onboardingRequest struct {
	TemplateIDs []string `json:"templateIds"`
}

// listOnboarding returns the org's onboarding auto-issue set.
func (h *Handler) listOnboarding(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	set, err := h.onboarding.ListOnboardingAttestations(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("listing onboarding attestations: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, set)
	return nil
}

// putOnboarding replaces the org's onboarding auto-issue set with the given
// templates, in order. Each must be one of the org's own natural-person templates.
func (h *Handler) putOnboarding(w http.ResponseWriter, r *http.Request) error {
	var req onboardingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	templateIDs := make([]uuid.UUID, len(req.TemplateIDs))
	for i, raw := range req.TemplateIDs {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil {
			return badRequest("invalid_input", "invalid templateId")
		}
		templateIDs[i] = id
	}

	org := organization.OrgFromContext(r.Context())
	set, err := h.onboarding.SetOnboardingAttestations(r.Context(), org.ID, templateIDs)
	switch {
	case errors.Is(err, ErrTemplateNotFound):
		return notFound("template_not_found", "template not found")
	case errors.Is(err, ErrOnboardingSubject):
		return badRequest("onboarding_subject_mismatch", "onboarding auto-issue is only supported for natural-person templates")
	case err != nil:
		return fmt.Errorf("updating onboarding attestations: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, set)
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
	if len(req.AttributeSources) > 0 {
		sc, err := h.schemas.GetSchema(r.Context(), org.ID, schemaID)
		if errors.Is(err, ErrSchemaNotFound) {
			return badRequest("invalid_input", "schema not found")
		}
		if err != nil {
			return fmt.Errorf("resolving schema for template attribute sources: %w", err)
		}
		if err := ValidateAttributeSources(sc.SubjectType, sc.Attributes, req.AttributeSources); err != nil {
			return badRequest("invalid_input", err.Error())
		}
	}
	t, err := h.templates.CreateTemplate(r.Context(), org.ID, Template{
		SchemaID: schemaID, Name: req.Name, DefaultAttributes: req.DefaultAttributes,
		AttributeSources: req.AttributeSources,
		ValiditySeconds:  req.ValiditySeconds, KeyMaterialID: keyID,
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
	if len(req.AttributeSources) > 0 {
		existing, err := h.templates.GetTemplate(r.Context(), org.ID, id)
		if errors.Is(err, ErrTemplateNotFound) {
			return notFound("template_not_found", "template not found")
		}
		if err != nil {
			return fmt.Errorf("resolving template for attribute sources: %w", err)
		}
		if err := ValidateAttributeSources(existing.SubjectType, existing.Attributes, req.AttributeSources); err != nil {
			return badRequest("invalid_input", err.Error())
		}
	}
	t, err := h.templates.UpdateTemplate(r.Context(), org.ID, id, Template{
		Name: req.Name, DefaultAttributes: req.DefaultAttributes,
		AttributeSources: req.AttributeSources,
		ValiditySeconds:  req.ValiditySeconds, KeyMaterialID: keyID, Status: req.Status,
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
	TemplateID     string            `json:"templateId"`
	Recipient      recipientRequest  `json:"recipient"`
	Attributes     map[string]string `json:"attributes"`
	DeliveryMethod string            `json:"deliveryMethod"`
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
	deliveryMethod := strings.TrimSpace(req.DeliveryMethod)
	if deliveryMethod != "" && deliveryMethod != DeliveryMethodEmail && deliveryMethod != DeliveryMethodQR {
		return badRequest("invalid_input", "invalid deliveryMethod")
	}

	org := organization.OrgFromContext(r.Context())
	actor := auth.UserFromContext(r.Context())
	result, err := h.service.Issue(r.Context(), org.ID, &actor.ID, org.Name, IssueInput{
		TemplateID:     templateID,
		Recipient:      Recipient{Kind: req.Recipient.Kind, UserID: userID, Ref: ref},
		Attributes:     req.Attributes,
		DeliveryMethod: deliveryMethod,
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
		return &respond.APIError{Status: http.StatusConflict, Code: "not_revocable", Message: "only a claimed credential can be revoked"}
	case err != nil:
		return fmt.Errorf("revoking attestation: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, issued)
	return nil
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r, "id", "attestation")
	if err != nil {
		return err
	}
	org := organization.OrgFromContext(r.Context())
	issued, err := h.service.Cancel(r.Context(), org.ID, id)
	switch {
	case errors.Is(err, ErrIssuedNotFound):
		return notFound("attestation_not_found", "attestation not found")
	case errors.Is(err, ErrNotOfferable):
		return &respond.APIError{Status: http.StatusConflict, Code: "not_cancellable", Message: "only an unclaimed offer can be cancelled"}
	case err != nil:
		return fmt.Errorf("cancelling attestation offer: %w", err)
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
