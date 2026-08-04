package email

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// The tenant-editing surface of the mail catalogue. Kinds and locales are code
// (catalog.go), so these routes never create or name a template — they only carry
// an org's copy for a kind/locale pair that already exists, which is why an
// unknown kind or locale is a 404 rather than a 400.

// maxTemplateBody caps a template save/preview payload. A template is a subject
// plus a bounded block layout of prose; 64 KiB is far above any real message and
// keeps a runaway body out of the JSON decoder. The block count itself is capped
// by ValidateTemplate (maxBlocks, render.go).
const maxTemplateBody = 64 << 10

// templateSummary is one cell of the kind × locale matrix in the list response.
type templateSummary struct {
	Locale string `json:"locale"`
	// Customized is true when the org has stored its own copy for this cell; false
	// means the response carries the shipped default.
	Customized bool       `json:"customized"`
	Subject    string     `json:"subject"`
	UpdatedAt  *time.Time `json:"updatedAt,omitempty"`
}

// templateKindSummary is one mail cause with its variable allowlist and the state
// of every locale.
type templateKindSummary struct {
	Kind      string             `json:"kind"`
	Variables []templateVariable `json:"variables"`
	Locales   []templateSummary  `json:"locales"`
}

// templateVariable is the wire shape of one allowed placeholder. It mirrors
// Variable field for field so the editor's palette can offer exactly what
// ValidateTemplate will accept, and marks URL variables so the editor knows which
// one may stand in for the call to action.
type templateVariable struct {
	Name  string `json:"name"`
	IsURL bool   `json:"isUrl"`
}

type templateListResponse struct {
	Kinds []templateKindSummary `json:"kinds"`
}

// templateResponse is one editable template: what is in force, what the shipped
// default says (so the editor can show a diff and offer a revert), and which
// placeholders are allowed.
type templateResponse struct {
	Kind       string             `json:"kind"`
	Locale     string             `json:"locale"`
	Customized bool               `json:"customized"`
	UpdatedAt  *time.Time         `json:"updatedAt,omitempty"`
	Template   Template           `json:"template"`
	Default    Template           `json:"default"`
	Variables  []templateVariable `json:"variables"`
}

type previewRequest struct {
	Locale string `json:"locale"`
	// Template is the unsaved draft to render. Absent renders what is in force.
	Template *Template `json:"template"`
}

type previewResponse struct {
	Subject string `json:"subject"`
	HTML    string `json:"html"`
	Text    string `json:"text"`
}

func (h *Handler) registerTemplateRoutes(mux *http.ServeMux, admin func(http.Handler) http.Handler) {
	mux.Handle("GET /orgs/{slug}/email/templates", admin(respond.HandlerFunc(h.listTemplates)))
	mux.Handle("GET /orgs/{slug}/email/templates/{kind}/{locale}", admin(respond.HandlerFunc(h.getTemplate)))
	mux.Handle("PUT /orgs/{slug}/email/templates/{kind}/{locale}", admin(respond.HandlerFunc(h.putTemplate)))
	mux.Handle("DELETE /orgs/{slug}/email/templates/{kind}/{locale}", admin(respond.HandlerFunc(h.deleteTemplate)))
	mux.Handle("POST /orgs/{slug}/email/templates/{kind}/preview", admin(respond.HandlerFunc(h.previewTemplate)))
}

// listTemplates returns the whole kind × locale matrix in one response: the
// editor needs every cell to show which causes a tenant has customised, and the
// matrix is a handful of rows.
func (h *Handler) listTemplates(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	overrides, err := h.store.ListTemplates(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("listing email templates: %w", err)
	}
	stored := make(map[string]TemplateOverride, len(overrides))
	for _, record := range overrides {
		stored[templateTargetID(record.Kind, record.Locale)] = record
	}

	out := templateListResponse{Kinds: make([]templateKindSummary, 0, len(Kinds()))}
	for _, kind := range Kinds() {
		summary := templateKindSummary{
			Kind:      string(kind),
			Variables: variablesFor(kind),
			Locales:   make([]templateSummary, 0, len(Locales())),
		}
		for _, locale := range Locales() {
			cell := templateSummary{Locale: string(locale)}
			if record, ok := stored[templateTargetID(kind, locale)]; ok {
				cell.Customized = true
				cell.Subject = record.Template.Subject
				updated := record.UpdatedAt
				cell.UpdatedAt = &updated
			} else {
				tpl, _ := DefaultTemplate(kind, locale)
				cell.Subject = tpl.Subject
			}
			summary.Locales = append(summary.Locales, cell)
		}
		out.Kinds = append(out.Kinds, summary)
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

func (h *Handler) getTemplate(w http.ResponseWriter, r *http.Request) error {
	kind, locale, err := templateTarget(r)
	if err != nil {
		return err
	}
	return h.respondWithTemplate(w, r, kind, locale, http.StatusOK)
}

func (h *Handler) putTemplate(w http.ResponseWriter, r *http.Request) error {
	kind, locale, err := templateTarget(r)
	if err != nil {
		return err
	}
	var tpl Template
	if err := decodeTemplateBody(w, r, &tpl); err != nil {
		return err
	}
	tpl = trimTemplate(tpl)

	org := organization.OrgFromContext(r.Context())
	if _, err := h.store.SaveTemplate(r.Context(), org.ID, kind, locale, tpl); err != nil {
		if invalid, ok := invalidTemplateError(err); ok {
			return invalid
		}
		return fmt.Errorf("saving email template: %w", err)
	}
	return h.respondWithTemplate(w, r, kind, locale, http.StatusOK)
}

// deleteTemplate reverts a cell to the shipped default and answers with the
// template now in force, so the editor reloads the default copy without a second
// round trip.
func (h *Handler) deleteTemplate(w http.ResponseWriter, r *http.Request) error {
	kind, locale, err := templateTarget(r)
	if err != nil {
		return err
	}
	org := organization.OrgFromContext(r.Context())
	deleted, err := h.store.DeleteTemplate(r.Context(), org.ID, kind, locale)
	if err != nil {
		return fmt.Errorf("resetting email template: %w", err)
	}
	if !deleted {
		return apiError(http.StatusNotFound, "not_found", "this template is not customized")
	}
	return h.respondWithTemplate(w, r, kind, locale, http.StatusOK)
}

// previewTemplate renders a draft (or what is in force) with the kind's sample
// variables and the org's branding. The editor shows this instead of building its
// own HTML, so a preview and a delivered message cannot drift apart.
func (h *Handler) previewTemplate(w http.ResponseWriter, r *http.Request) error {
	kind, ok := parseKind(r.PathValue("kind"))
	if !ok {
		return unknownTemplate()
	}
	var req previewRequest
	if err := decodeTemplateBody(w, r, &req); err != nil {
		return err
	}
	locale, ok := ParseLocale(req.Locale)
	if !ok {
		return badRequest("invalid_input", "locale must be one of the supported mail locales")
	}
	if req.Template != nil {
		trimmed := trimTemplate(*req.Template)
		req.Template = &trimmed
	}

	org := organization.OrgFromContext(r.Context())
	body, err := h.service.Preview(r.Context(), org.ID, kind, locale, req.Template, org.Name)
	if err != nil {
		if invalid, ok := invalidTemplateError(err); ok {
			return invalid
		}
		return fmt.Errorf("previewing email template: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, previewResponse{Subject: body.Subject, HTML: body.HTMLBody, Text: body.TextBody})
	return nil
}

func (h *Handler) respondWithTemplate(w http.ResponseWriter, r *http.Request, kind Kind, locale Locale, status int) error {
	org := organization.OrgFromContext(r.Context())
	record, customized, err := h.store.GetTemplate(r.Context(), org.ID, kind, locale)
	if err != nil {
		return fmt.Errorf("getting email template: %w", err)
	}
	shipped, _ := DefaultTemplate(kind, locale)

	out := templateResponse{
		Kind:       string(kind),
		Locale:     string(locale),
		Customized: customized,
		Template:   shipped,
		Default:    shipped,
		Variables:  variablesFor(kind),
	}
	if customized {
		out.Template = record.Template
		updated := record.UpdatedAt
		out.UpdatedAt = &updated
	}
	respond.JSON(w, r, status, out)
	return nil
}

// templateTarget reads the {kind} and {locale} path segments. Both are closed sets
// owned by the backend, so an unrecognised value is a request for something that
// does not exist rather than malformed input.
func templateTarget(r *http.Request) (Kind, Locale, error) {
	kind, ok := parseKind(r.PathValue("kind"))
	if !ok {
		return "", "", unknownTemplate()
	}
	locale, ok := ParseLocale(r.PathValue("locale"))
	if !ok {
		return "", "", unknownTemplate()
	}
	return kind, locale, nil
}

func parseKind(value string) (Kind, bool) {
	for _, kind := range Kinds() {
		if string(kind) == value {
			return kind, true
		}
	}
	return "", false
}

func unknownTemplate() error {
	return apiError(http.StatusNotFound, "not_found", "unknown mail template")
}

func variablesFor(kind Kind) []templateVariable {
	variables, _ := VariablesFor(kind)
	out := make([]templateVariable, 0, len(variables))
	for _, v := range variables {
		out = append(out, templateVariable(v))
	}
	return out
}

// invalidTemplateError turns a rejected template into a 400 that names the field
// and the reason, because the editor shows that message next to the field the
// tenant is typing in.
func invalidTemplateError(err error) (error, bool) {
	invalid, ok := errors.AsType[*InvalidTemplateError](err)
	if !ok {
		return nil, false
	}
	return badRequest("invalid_template", invalid.Reason.Error()), true
}

func decodeTemplateBody(w http.ResponseWriter, r *http.Request, into any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxTemplateBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return apiError(http.StatusRequestEntityTooLarge, "payload_too_large", "the template is too large")
		}
		return badRequest("invalid_body", "invalid request body")
	}
	return nil
}

// trimTemplate normalises tenant input the way the settings forms do: surrounding
// whitespace is never meaningful in a mail field. Blocks are trimmed, not
// dropped — a block the tenant added but left empty comes back as a validation
// error naming it, rather than vanishing from the layout on save.
func trimTemplate(tpl Template) Template {
	out := Template{
		Subject:   strings.TrimSpace(tpl.Subject),
		Preheader: strings.TrimSpace(tpl.Preheader),
	}
	for _, blk := range tpl.Blocks {
		out.Blocks = append(out.Blocks, Block{
			Type:         blk.Type,
			Text:         strings.TrimSpace(blk.Text),
			Label:        strings.TrimSpace(blk.Label),
			URL:          strings.TrimSpace(blk.URL),
			LinkFallback: strings.TrimSpace(blk.LinkFallback),
		})
	}
	return out
}
