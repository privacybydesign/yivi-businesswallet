package attestation

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

const schemaColumns = `id, organization_id, vct, display_name, credential_config_id,
	subject_type, attributes, display, qualified, status, created_at, updated_at`

func scanSchema(row rowScanner) (Schema, error) {
	var (
		s          Schema
		attrsRaw   []byte
		displayRaw []byte
	)
	if err := row.Scan(
		&s.ID, &s.OrganizationID, &s.VCT, &s.DisplayName, &s.CredentialConfigID,
		&s.SubjectType, &attrsRaw, &displayRaw, &s.Qualified, &s.Status, &s.CreatedAt, &s.UpdatedAt,
	); err != nil {
		return Schema{}, err
	}
	attrs, err := unmarshalAttributes(attrsRaw)
	if err != nil {
		return Schema{}, err
	}
	s.Attributes = attrs
	display, err := unmarshalNames(displayRaw)
	if err != nil {
		return Schema{}, err
	}
	s.Display = display
	return s, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

// ListSchemas returns an organization's schemas, newest first.
func (s *Store) ListSchemas(ctx context.Context, orgID uuid.UUID) ([]Schema, error) {
	const query = `SELECT ` + schemaColumns + ` FROM attestation_schemas WHERE organization_id = $1 ORDER BY created_at DESC`
	rows, err := s.db.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("attestation: list schemas org %s: %w", orgID, err)
	}
	defer rows.Close()

	schemas := []Schema{}
	for rows.Next() {
		sc, err := scanSchema(rows)
		if err != nil {
			return nil, fmt.Errorf("attestation: list schemas scan: %w", err)
		}
		schemas = append(schemas, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("attestation: list schemas rows: %w", err)
	}
	return schemas, nil
}

// GetSchema returns one org-scoped schema.
func (s *Store) GetSchema(ctx context.Context, orgID, id uuid.UUID) (Schema, error) {
	const query = `SELECT ` + schemaColumns + ` FROM attestation_schemas WHERE organization_id = $1 AND id = $2`
	sc, err := scanSchema(s.db.QueryRow(ctx, query, orgID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Schema{}, ErrSchemaNotFound
	}
	if err != nil {
		return Schema{}, fmt.Errorf("attestation: get schema %s: %w", id, err)
	}
	return sc, nil
}

// CreateSchema inserts a schema and audits, in one transaction.
func (s *Store) CreateSchema(ctx context.Context, orgID uuid.UUID, in Schema) (Schema, error) {
	attrs, err := marshalJSON(in.Attributes)
	if err != nil {
		return Schema{}, err
	}
	display, err := marshalJSON(in.Display)
	if err != nil {
		return Schema{}, err
	}
	status := in.Status
	if status == "" {
		status = SchemaActive
	}
	subjectType := in.SubjectType
	if subjectType == "" {
		subjectType = SubjectNaturalPerson
	}

	var out Schema
	err = database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO attestation_schemas
			(organization_id, vct, display_name, credential_config_id, subject_type, attributes, display, qualified, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			RETURNING ` + schemaColumns
		var err error
		out, err = scanSchema(q.QueryRow(ctx, insert, orgID, in.VCT, in.DisplayName, in.CredentialConfigID, subjectType, attrs, display, in.Qualified, status))
		if isUniqueViolation(err) {
			return ErrSchemaVctTaken
		}
		if err != nil {
			return fmt.Errorf("attestation: create schema org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.AttestationSchemaCreated,
			audit.Target{Type: audit.TargetAttestationSchema, ID: out.ID.String(), OrgID: &orgID},
			audit.Created(map[string]any{"vct": out.VCT, "displayName": out.DisplayName, "qualified": out.Qualified}))
	})
	return out, err
}

// UpdateSchema updates the mutable fields of a schema and audits, in one tx.
func (s *Store) UpdateSchema(ctx context.Context, orgID, id uuid.UUID, in Schema) (Schema, error) {
	attrs, err := marshalJSON(in.Attributes)
	if err != nil {
		return Schema{}, err
	}
	display, err := marshalJSON(in.Display)
	if err != nil {
		return Schema{}, err
	}

	var out Schema
	err = database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE attestation_schemas
			SET display_name = $3, credential_config_id = $4, subject_type = $5, attributes = $6, display = $9, qualified = $7, status = $8, updated_at = now()
			WHERE organization_id = $1 AND id = $2
			RETURNING ` + schemaColumns
		var err error
		out, err = scanSchema(q.QueryRow(ctx, update, orgID, id, in.DisplayName, in.CredentialConfigID, in.SubjectType, attrs, in.Qualified, in.Status, display))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSchemaNotFound
		}
		if err != nil {
			return fmt.Errorf("attestation: update schema %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.AttestationSchemaUpdated,
			audit.Target{Type: audit.TargetAttestationSchema, ID: out.ID.String(), OrgID: &orgID},
			audit.Updated(nil, map[string]any{"displayName": out.DisplayName, "status": out.Status}))
	})
	return out, err
}

// DeleteSchema removes a schema and audits, in one tx.
func (s *Store) DeleteSchema(ctx context.Context, orgID, id uuid.UUID) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const del = `DELETE FROM attestation_schemas WHERE organization_id = $1 AND id = $2 RETURNING vct`
		var vct string
		if err := q.QueryRow(ctx, del, orgID, id).Scan(&vct); errors.Is(err, pgx.ErrNoRows) {
			return ErrSchemaNotFound
		} else if err != nil {
			return fmt.Errorf("attestation: delete schema %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.AttestationSchemaDeleted,
			audit.Target{Type: audit.TargetAttestationSchema, ID: id.String(), OrgID: &orgID},
			audit.Deleted(map[string]any{"vct": vct}))
	})
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation
}
