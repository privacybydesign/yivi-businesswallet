package attestation

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation/rulebook"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// rulebookCredentialConfigPrefix prefixes the default issuer credential config id
// of an imported rulebook schema.
const rulebookCredentialConfigPrefix = "we-build-"

// RulebookView is a catalogue entry as the schema import dialog shows it, with a
// sample of valid claims to prefill the issue form.
type RulebookView struct {
	ID          string         `json:"id"`
	VCT         string         `json:"vct"`
	Name        string         `json:"name"`
	SubjectType string         `json:"subjectType"`
	Upstream    string         `json:"upstream"`
	Attributes  []AttributeDef `json:"attributes"`
	Sample      map[string]any `json:"sample"`
}

func rulebookView(rb *rulebook.Entry) RulebookView {
	return RulebookView{
		ID: rb.ID, VCT: rb.VCT, Name: rb.Name, SubjectType: rb.SubjectType, Upstream: rb.Upstream,
		Attributes: rulebookAttributes(rb), Sample: rb.Sample(),
	}
}

func rulebookAttributes(rb *rulebook.Entry) []AttributeDef {
	attrs := rb.Attributes()
	out := make([]AttributeDef, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, AttributeDef{Key: a.Key, Label: a.Label, Type: a.Type, Required: a.Required})
	}
	return out
}

// SchemaFromRulebook builds the schema an organization holds for a rulebook. An
// empty credentialConfigID defaults to "we-build-<rulebook id>".
func SchemaFromRulebook(id, credentialConfigID string) (Schema, error) {
	rb, ok := rulebook.Get(id)
	if !ok {
		return Schema{}, fmt.Errorf("%w: %q", ErrUnknownRulebook, id)
	}
	if credentialConfigID == "" {
		credentialConfigID = rulebookCredentialConfigPrefix + rb.ID
	}
	return Schema{
		VCT:                rb.VCT,
		DisplayName:        rb.Name,
		CredentialConfigID: credentialConfigID,
		SubjectType:        rb.SubjectType,
		Attributes:         rulebookAttributes(rb),
		Display:            []LocalizedName{{Lang: defaultVctLanguage, Name: rb.Name}},
		Status:             SchemaActive,
		Rulebook:           rb.ID,
	}, nil
}

// keepRulebookFields carries a rulebook schema's fixed fields over an update.
func keepRulebookFields(existing, in Schema) Schema {
	if existing.Rulebook == "" {
		return in
	}
	in.SubjectType = existing.SubjectType
	in.Attributes = existing.Attributes
	return in
}

func (h *Handler) listRulebooks(w http.ResponseWriter, r *http.Request) error {
	entries := rulebook.All()
	out := make([]RulebookView, 0, len(entries))
	for _, rb := range entries {
		out = append(out, rulebookView(rb))
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

type importRulebookRequest struct {
	CredentialConfigID string `json:"credentialConfigId"`
}

func (h *Handler) importRulebook(w http.ResponseWriter, r *http.Request) error {
	var req importRulebookRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return badRequest("invalid_body", "invalid request body")
		}
	}
	schema, err := SchemaFromRulebook(r.PathValue("id"), strings.TrimSpace(req.CredentialConfigID))
	if errors.Is(err, ErrUnknownRulebook) {
		return notFound("rulebook_not_found", "rulebook not found")
	}
	if err != nil {
		return err
	}
	org := organization.OrgFromContext(r.Context())
	sc, err := h.schemas.CreateSchema(r.Context(), org.ID, schema)
	if errors.Is(err, ErrSchemaVctTaken) {
		return &respond.APIError{Status: http.StatusConflict, Code: "vct_taken", Message: "this rulebook is already imported"}
	}
	if err != nil {
		return fmt.Errorf("importing rulebook schema: %w", err)
	}
	sc.LogoURI = schemaLogoURL(org.Slug, sc)
	respond.JSON(w, r, http.StatusCreated, sc)
	return nil
}
