package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// PostgresStore is the Store on identity_proofing_flow_versions. A flow's
// TenantID is its organization's id.
type PostgresStore struct {
	db database.DB
}

// NewPostgresStore returns the flow Store on db.
func NewPostgresStore(db database.DB) *PostgresStore { return &PostgresStore{db: db} }

func (s *PostgresStore) Save(ctx context.Context, fd FlowDefinition) (FlowDefinition, error) {
	if err := Validate(fd); err != nil {
		return FlowDefinition{}, err
	}
	if fd.TenantID == "" {
		return FlowDefinition{}, errors.New("flow: tenantId is required")
	}
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		if fd.ID == "" {
			fd.ID, fd.Version = newID(), 1
		} else {
			var maxVersion *int
			if err := q.QueryRow(ctx,
				`SELECT MAX(version) FROM identity_proofing_flow_versions WHERE organization_id = $1 AND flow_id = $2`,
				fd.TenantID, fd.ID).Scan(&maxVersion); err != nil {
				return err
			}
			if maxVersion == nil {
				return ErrNotFound
			}
			fd.Version = *maxVersion + 1
		}
		fd.CreatedAt, fd.Active = time.Now().UTC(), true
		def, err := json.Marshal(fd)
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx,
			`UPDATE identity_proofing_flow_versions SET active = false WHERE organization_id = $1 AND flow_id = $2`,
			fd.TenantID, fd.ID); err != nil {
			return err
		}
		_, err = q.Exec(ctx, `
			INSERT INTO identity_proofing_flow_versions (organization_id, flow_id, version, definition, active, created_at)
			VALUES ($1, $2, $3, $4, true, $5)`, fd.TenantID, fd.ID, fd.Version, def, fd.CreatedAt)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return FlowDefinition{}, err
		}
		return FlowDefinition{}, fmt.Errorf("flow: save: %w", err)
	}
	return fd, nil
}

const selectFlowSQL = `SELECT definition, active FROM identity_proofing_flow_versions`

func (s *PostgresStore) Get(ctx context.Context, tenantID, flowID string, version int) (FlowDefinition, error) {
	var row pgx.Row
	if version == 0 {
		row = s.db.QueryRow(ctx, selectFlowSQL+` WHERE organization_id = $1 AND flow_id = $2 AND active`, tenantID, flowID)
	} else {
		row = s.db.QueryRow(ctx, selectFlowSQL+` WHERE organization_id = $1 AND flow_id = $2 AND version = $3`, tenantID, flowID, version)
	}
	return scanFlow(row)
}

func (s *PostgresStore) ListVersions(ctx context.Context, tenantID, flowID string) ([]FlowDefinition, error) {
	return s.list(ctx, selectFlowSQL+` WHERE organization_id = $1 AND flow_id = $2 ORDER BY version`, tenantID, flowID)
}

func (s *PostgresStore) List(ctx context.Context, tenantID string) ([]FlowDefinition, error) {
	return s.list(ctx, selectFlowSQL+` WHERE organization_id = $1 AND active ORDER BY created_at DESC`, tenantID)
}

func (s *PostgresStore) Activate(ctx context.Context, tenantID, flowID string, version int) error {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM identity_proofing_flow_versions
			WHERE organization_id = $1 AND flow_id = $2 AND version = $3)`, tenantID, flowID, version).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		// Clear first: the partial unique index allows one active row at a time.
		if _, err := q.Exec(ctx, `UPDATE identity_proofing_flow_versions SET active = false
			WHERE organization_id = $1 AND flow_id = $2 AND active`, tenantID, flowID); err != nil {
			return err
		}
		_, err := q.Exec(ctx, `UPDATE identity_proofing_flow_versions SET active = true
			WHERE organization_id = $1 AND flow_id = $2 AND version = $3`, tenantID, flowID, version)
		return err
	})
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("flow: activate: %w", err)
	}
	return err
}

func (s *PostgresStore) list(ctx context.Context, query string, args ...any) ([]FlowDefinition, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("flow: list: %w", err)
	}
	defer rows.Close()
	out := make([]FlowDefinition, 0)
	for rows.Next() {
		fd, err := scanFlow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, fd)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("flow: list: %w", err)
	}
	return out, nil
}

func scanFlow(row pgx.Row) (FlowDefinition, error) {
	var def []byte
	var active bool
	if err := row.Scan(&def, &active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return FlowDefinition{}, ErrNotFound
		}
		return FlowDefinition{}, fmt.Errorf("flow: read: %w", err)
	}
	var fd FlowDefinition
	if err := json.Unmarshal(def, &fd); err != nil {
		return FlowDefinition{}, fmt.Errorf("flow: decode: %w", err)
	}
	fd.Active = active
	return fd, nil
}
