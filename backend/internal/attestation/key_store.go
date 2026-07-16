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

const keyColumns = `id, organization_id, kind, label, provider_ref, status, created_at, updated_at`

func scanKey(row rowScanner) (Key, error) {
	var k Key
	if err := row.Scan(&k.ID, &k.OrganizationID, &k.Kind, &k.Label, &k.ProviderRef, &k.Status, &k.CreatedAt, &k.UpdatedAt); err != nil {
		return Key{}, err
	}
	return k, nil
}

// ListKeys returns an organization's key material, newest first.
func (s *Store) ListKeys(ctx context.Context, orgID uuid.UUID) ([]Key, error) {
	const query = `SELECT ` + keyColumns + ` FROM attestation_keys WHERE organization_id = $1 ORDER BY created_at DESC`
	rows, err := s.db.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("attestation: list keys org %s: %w", orgID, err)
	}
	defer rows.Close()

	keys := []Key{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, fmt.Errorf("attestation: list keys scan: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("attestation: list keys rows: %w", err)
	}
	return keys, nil
}

// CreateKey inserts a key-material reference and audits, in one tx.
func (s *Store) CreateKey(ctx context.Context, orgID uuid.UUID, kind, label, providerRef string) (Key, error) {
	var out Key
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO attestation_keys (organization_id, kind, label, provider_ref)
			VALUES ($1, $2, $3, $4)
			RETURNING ` + keyColumns
		var err error
		out, err = scanKey(q.QueryRow(ctx, insert, orgID, kind, label, providerRef))
		if err != nil {
			return fmt.Errorf("attestation: create key org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.AttestationKeyAdded,
			audit.Target{Type: audit.TargetAttestationKey, ID: out.ID.String(), OrgID: &orgID},
			audit.Created(map[string]any{"kind": out.Kind, "label": out.Label}))
	})
	return out, err
}

// SetKeyStatus transitions a key to suspended or revoked and audits, in one tx.
func (s *Store) SetKeyStatus(ctx context.Context, orgID, id uuid.UUID, status, action string) (Key, error) {
	var out Key
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE attestation_keys SET status = $3, updated_at = now()
			WHERE organization_id = $1 AND id = $2
			RETURNING ` + keyColumns
		var err error
		out, err = scanKey(q.QueryRow(ctx, update, orgID, id, status))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrKeyNotFound
		}
		if err != nil {
			return fmt.Errorf("attestation: set key status %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, action,
			audit.Target{Type: audit.TargetAttestationKey, ID: out.ID.String(), OrgID: &orgID},
			audit.Updated(nil, map[string]any{"status": out.Status}))
	})
	return out, err
}
