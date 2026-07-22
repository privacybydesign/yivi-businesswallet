package attestation

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// templateSelect joins the schema identity + attribute chips and counts issued
// attestations, so the Templates tab renders from a single query.
const templateSelect = `SELECT t.id, t.organization_id, t.schema_id, t.name,
		t.default_attributes, t.attribute_sources, t.validity_seconds, t.key_material_id, t.status, t.created_at, t.updated_at,
		s.vct, s.display_name, s.subject_type, s.attributes, s.qualified,
		COUNT(ia.id) AS issued_count
	FROM attestation_templates t
	JOIN attestation_schemas s ON s.id = t.schema_id
	LEFT JOIN issued_attestations ia ON ia.template_id = t.id
	WHERE t.organization_id = $1`

const templateGroupOrder = ` GROUP BY t.id, s.id`

func scanTemplate(row rowScanner) (Template, error) {
	var (
		t               Template
		defaultAttrsRaw []byte
		sourcesRaw      []byte
		schemaAttrsRaw  []byte
		schemaDisplay   string
		validitySeconds *int
		keyMaterialID   *uuid.UUID
	)
	if err := row.Scan(
		&t.ID, &t.OrganizationID, &t.SchemaID, &t.Name,
		&defaultAttrsRaw, &sourcesRaw, &validitySeconds, &keyMaterialID, &t.Status, &t.CreatedAt, &t.UpdatedAt,
		&t.VCT, &schemaDisplay, &t.SubjectType, &schemaAttrsRaw, &t.Qualified,
		&t.IssuedCount,
	); err != nil {
		return Template{}, err
	}
	t.ValiditySeconds = validitySeconds
	t.KeyMaterialID = keyMaterialID
	t.DisplayName = schemaDisplay

	defaults, err := unmarshalStringMap(defaultAttrsRaw)
	if err != nil {
		return Template{}, err
	}
	if len(defaults) > 0 {
		t.DefaultAttributes = defaults
	}
	sources, err := unmarshalStringMap(sourcesRaw)
	if err != nil {
		return Template{}, err
	}
	if len(sources) > 0 {
		t.AttributeSources = sources
	}
	attrs, err := unmarshalAttributes(schemaAttrsRaw)
	if err != nil {
		return Template{}, err
	}
	t.Attributes = attrs
	return t, nil
}

// ListTemplates returns an organization's templates enriched for the cards.
func (s *Store) ListTemplates(ctx context.Context, orgID uuid.UUID) ([]Template, error) {
	rows, err := s.db.Query(ctx, templateSelect+templateGroupOrder+` ORDER BY t.created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("attestation: list templates org %s: %w", orgID, err)
	}
	defer rows.Close()

	templates := []Template{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, fmt.Errorf("attestation: list templates scan: %w", err)
		}
		templates = append(templates, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("attestation: list templates rows: %w", err)
	}
	return templates, nil
}

// GetTemplate returns one enriched template.
func (s *Store) GetTemplate(ctx context.Context, orgID, id uuid.UUID) (Template, error) {
	t, err := scanTemplate(s.db.QueryRow(ctx, templateSelect+` AND t.id = $2`+templateGroupOrder, orgID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Template{}, ErrTemplateNotFound
	}
	if err != nil {
		return Template{}, fmt.Errorf("attestation: get template %s: %w", id, err)
	}
	return t, nil
}

// GetTemplateDetail resolves the template + schema fields the issue flow needs.
func (s *Store) GetTemplateDetail(ctx context.Context, orgID, id uuid.UUID) (TemplateDetail, error) {
	const query = `SELECT t.id, t.organization_id, t.name, t.default_attributes, t.validity_seconds,
			s.vct, s.credential_config_id, s.subject_type, s.attributes, s.qualified
		FROM attestation_templates t
		JOIN attestation_schemas s ON s.id = t.schema_id
		WHERE t.organization_id = $1 AND t.id = $2`
	var (
		d               TemplateDetail
		defaultAttrsRaw []byte
		schemaAttrsRaw  []byte
		validitySeconds *int
	)
	err := s.db.QueryRow(ctx, query, orgID, id).Scan(
		&d.ID, &d.OrganizationID, &d.Name, &defaultAttrsRaw, &validitySeconds,
		&d.SchemaVCT, &d.CredentialConfigID, &d.SubjectType, &schemaAttrsRaw, &d.Qualified,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return TemplateDetail{}, ErrTemplateNotFound
	}
	if err != nil {
		return TemplateDetail{}, fmt.Errorf("attestation: get template detail %s: %w", id, err)
	}
	d.ValiditySeconds = validitySeconds
	if d.DefaultAttributes, err = unmarshalStringMap(defaultAttrsRaw); err != nil {
		return TemplateDetail{}, err
	}
	if d.SchemaAttributes, err = unmarshalAttributes(schemaAttrsRaw); err != nil {
		return TemplateDetail{}, err
	}
	return d, nil
}

// CreateTemplate inserts a template and audits, in one tx, then returns it enriched.
func (s *Store) CreateTemplate(ctx context.Context, orgID uuid.UUID, in Template) (Template, error) {
	defaults, err := marshalStringMapOrEmpty(in.DefaultAttributes)
	if err != nil {
		return Template{}, err
	}
	sources, err := marshalStringMapOrEmpty(in.AttributeSources)
	if err != nil {
		return Template{}, err
	}

	var id uuid.UUID
	err = database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO attestation_templates
			(organization_id, schema_id, name, default_attributes, attribute_sources, validity_seconds, key_material_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id`
		if err := q.QueryRow(ctx, insert, orgID, in.SchemaID, in.Name, defaults, sources, in.ValiditySeconds, in.KeyMaterialID).Scan(&id); err != nil {
			// A missing schema (or key) surfaces as a foreign-key violation.
			if isForeignKeyViolation(err) {
				return ErrSchemaNotFound
			}
			return fmt.Errorf("attestation: create template org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.AttestationTemplateCreated,
			audit.Target{Type: audit.TargetAttestationTemplate, ID: id.String(), OrgID: &orgID},
			audit.Created(map[string]any{"name": in.Name, "schemaId": in.SchemaID.String()}))
	})
	if err != nil {
		return Template{}, err
	}
	return s.GetTemplate(ctx, orgID, id)
}

// UpdateTemplate updates mutable fields and audits, in one tx, then returns it enriched.
func (s *Store) UpdateTemplate(ctx context.Context, orgID, id uuid.UUID, in Template) (Template, error) {
	defaults, err := marshalStringMapOrEmpty(in.DefaultAttributes)
	if err != nil {
		return Template{}, err
	}
	sources, err := marshalStringMapOrEmpty(in.AttributeSources)
	if err != nil {
		return Template{}, err
	}
	status := in.Status
	if status == "" {
		status = TemplateActive
	}

	err = database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE attestation_templates
			SET name = $3, default_attributes = $4, attribute_sources = $5, validity_seconds = $6, key_material_id = $7, status = $8, updated_at = now()
			WHERE organization_id = $1 AND id = $2
			RETURNING id`
		var got uuid.UUID
		if err := q.QueryRow(ctx, update, orgID, id, in.Name, defaults, sources, in.ValiditySeconds, in.KeyMaterialID, status).Scan(&got); errors.Is(err, pgx.ErrNoRows) {
			return ErrTemplateNotFound
		} else if err != nil {
			return fmt.Errorf("attestation: update template %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.AttestationTemplateUpdated,
			audit.Target{Type: audit.TargetAttestationTemplate, ID: id.String(), OrgID: &orgID},
			audit.Updated(nil, map[string]any{"name": in.Name, "status": status}))
	})
	if err != nil {
		return Template{}, err
	}
	return s.GetTemplate(ctx, orgID, id)
}

// DeleteTemplate removes a template and audits, in one tx.
func (s *Store) DeleteTemplate(ctx context.Context, orgID, id uuid.UUID) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const del = `DELETE FROM attestation_templates WHERE organization_id = $1 AND id = $2 RETURNING name`
		var name string
		if err := q.QueryRow(ctx, del, orgID, id).Scan(&name); errors.Is(err, pgx.ErrNoRows) {
			return ErrTemplateNotFound
		} else if err != nil {
			return fmt.Errorf("attestation: delete template %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.AttestationTemplateDeleted,
			audit.Target{Type: audit.TargetAttestationTemplate, ID: id.String(), OrgID: &orgID},
			audit.Deleted(map[string]any{"name": name}))
	})
}
