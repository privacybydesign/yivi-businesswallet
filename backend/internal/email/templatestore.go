package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// Tenant-edited mail copy. org_email_templates holds only the (kind, locale)
// pairs an organization has actually customised: no row means "use the shipped
// default", so improving templates/defaults.<locale>.json still reaches every org
// that has not overridden that cause. Resetting a template is therefore a DELETE,
// not a write of the current default.

// TemplateOverride is one stored, tenant-edited template.
type TemplateOverride struct {
	Kind      Kind
	Locale    Locale
	Template  Template
	UpdatedAt time.Time
}

// templateColumns is the projection every read below shares, in the order
// scanTemplate expects.
const templateColumns = `kind, locale, subject, preheader, blocks, updated_at`

// ListTemplates returns the org's customised templates, ordered by kind then
// locale. Kinds and locales the org has not edited are absent rather than
// materialised from the defaults — the caller composes the full matrix with
// Kinds(), Locales() and DefaultTemplate.
func (s *Store) ListTemplates(ctx context.Context, orgID uuid.UUID) ([]TemplateOverride, error) {
	const query = `SELECT ` + templateColumns + `
		FROM org_email_templates WHERE organization_id = $1 ORDER BY kind, locale`
	rows, err := s.db.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("email: list templates org %s: %w", orgID, err)
	}
	defer rows.Close()

	var out []TemplateOverride
	for rows.Next() {
		record, err := scanTemplate(rows)
		if err != nil {
			return nil, fmt.Errorf("email: list templates org %s: %w", orgID, err)
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("email: list templates org %s: %w", orgID, err)
	}
	return out, nil
}

// GetTemplate returns the org's override for one kind and locale. ok is false when
// the org has not customised it, which is not an error: the caller falls back to
// the shipped default.
func (s *Store) GetTemplate(ctx context.Context, orgID uuid.UUID, kind Kind, locale Locale) (TemplateOverride, bool, error) {
	const query = `SELECT ` + templateColumns + `
		FROM org_email_templates
		WHERE organization_id = $1 AND kind = $2 AND locale = $3`
	record, err := scanTemplate(s.db.QueryRow(ctx, query, orgID, string(kind), string(locale)))
	if errors.Is(err, pgx.ErrNoRows) {
		return TemplateOverride{}, false, nil
	}
	if err != nil {
		return TemplateOverride{}, false, fmt.Errorf("email: get template org %s kind %s locale %s: %w", orgID, kind, locale, err)
	}
	return record, true, nil
}

// SaveTemplate stores a tenant's edit of one kind in one locale and audits it, in
// one transaction. The template is validated against the kind's variable
// allowlist first, so a placeholder that would render blank (or a call-to-action
// URL that would not be a link) is rejected here rather than at send time.
func (s *Store) SaveTemplate(ctx context.Context, orgID uuid.UUID, kind Kind, locale Locale, tpl Template) (TemplateOverride, error) {
	if err := ValidateTemplate(kind, tpl); err != nil {
		return TemplateOverride{}, &InvalidTemplateError{Reason: err}
	}
	blocks, err := json.Marshal(tpl.Blocks)
	if err != nil {
		return TemplateOverride{}, fmt.Errorf("email: save template org %s kind %s locale %s: encoding blocks: %w", orgID, kind, locale, err)
	}
	err = database.InTx(ctx, s.db, func(q database.Querier) error {
		const upsert = `INSERT INTO org_email_templates
			(organization_id, kind, locale, subject, preheader, blocks)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (organization_id, kind, locale) DO UPDATE SET
				subject = EXCLUDED.subject, preheader = EXCLUDED.preheader,
				blocks = EXCLUDED.blocks, updated_at = now()`
		if _, err := q.Exec(ctx, upsert, orgID, string(kind), string(locale),
			tpl.Subject, tpl.Preheader, blocks); err != nil {
			return fmt.Errorf("email: save template org %s kind %s locale %s: %w", orgID, kind, locale, err)
		}
		return s.audit.Record(ctx, q, audit.EmailTemplateUpdated,
			audit.Target{Type: audit.TargetEmailTemplate, ID: templateTargetID(kind, locale), OrgID: &orgID},
			// The body is tenant prose, so the detail records which template moved and
			// its subject, not the whole message.
			audit.Updated(nil, map[string]any{"kind": string(kind), "locale": string(locale), "subject": tpl.Subject}))
	})
	if err != nil {
		return TemplateOverride{}, err
	}
	record, ok, err := s.GetTemplate(ctx, orgID, kind, locale)
	if err != nil {
		return TemplateOverride{}, err
	}
	if !ok {
		return TemplateOverride{}, fmt.Errorf("email: template org %s kind %s locale %s vanished after save", orgID, kind, locale)
	}
	return record, nil
}

// DeleteTemplate reverts one kind in one locale to the shipped default by
// dropping the override, and audits it. ok is false when there was nothing to
// revert, so the caller can answer 404 instead of auditing a no-op.
func (s *Store) DeleteTemplate(ctx context.Context, orgID uuid.UUID, kind Kind, locale Locale) (bool, error) {
	deleted := false
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const del = `DELETE FROM org_email_templates
			WHERE organization_id = $1 AND kind = $2 AND locale = $3`
		tag, err := q.Exec(ctx, del, orgID, string(kind), string(locale))
		if err != nil {
			return fmt.Errorf("email: delete template org %s kind %s locale %s: %w", orgID, kind, locale, err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		deleted = true
		return s.audit.Record(ctx, q, audit.EmailTemplateReset,
			audit.Target{Type: audit.TargetEmailTemplate, ID: templateTargetID(kind, locale), OrgID: &orgID},
			audit.Updated(nil, map[string]any{"kind": string(kind), "locale": string(locale)}))
	})
	if err != nil {
		return false, err
	}
	return deleted, nil
}

// ResolveTemplate is what a send uses: the org's override when it has one, the
// shipped default otherwise. It implements the service's templateSource.
func (s *Store) ResolveTemplate(ctx context.Context, orgID uuid.UUID, kind Kind, locale Locale) (Template, error) {
	record, ok, err := s.GetTemplate(ctx, orgID, kind, locale)
	if err != nil {
		return Template{}, err
	}
	if ok {
		return record.Template, nil
	}
	tpl, ok := DefaultTemplate(kind, locale)
	if !ok {
		return Template{}, fmt.Errorf("email: no template for kind %q", kind)
	}
	return tpl, nil
}

// templateTargetID identifies the audited template. The row's own UUID is an
// implementation detail that changes on a reset-then-edit, whereas (kind, locale)
// is what an admin reading the audit log recognises.
func templateTargetID(kind Kind, locale Locale) string {
	return fmt.Sprintf("%s/%s", kind, locale)
}

// row is the shared shape of pgx.Row and pgx.Rows for a single-record scan.
type row interface {
	Scan(dest ...any) error
}

// scanTemplate reads one row. Kind and Locale are scanned as plain strings and
// converted, rather than relying on pgx to map TEXT onto a named string type; the
// block layout is decoded from its JSONB column explicitly for the same reason.
func scanTemplate(r row) (TemplateOverride, error) {
	var (
		out    TemplateOverride
		kind   string
		locale string
		blocks []byte
	)
	err := r.Scan(&kind, &locale, &out.Template.Subject, &out.Template.Preheader,
		&blocks, &out.UpdatedAt)
	if err != nil {
		return TemplateOverride{}, err
	}
	if err := json.Unmarshal(blocks, &out.Template.Blocks); err != nil {
		return TemplateOverride{}, fmt.Errorf("decoding blocks: %w", err)
	}
	out.Kind = Kind(kind)
	out.Locale = Locale(locale)
	return out, nil
}
