package qerds

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

const addressColumns = `id, organization_id, address, is_default, provider_ref, created_at`

func scanAddress(row rowScanner) (Address, error) {
	var (
		a           Address
		providerRef *string
	)
	if err := row.Scan(&a.ID, &a.OrganizationID, &a.Address, &a.IsDefault, &providerRef, &a.CreatedAt); err != nil {
		return Address{}, err
	}
	if providerRef != nil {
		a.ProviderRef = *providerRef
	}
	return a, nil
}

// ProvisionAddress assigns a digital address to an organization. When
// makeDefault is set, any existing default is cleared first so the partial
// unique index (one default per org) holds.
func (s *Store) ProvisionAddress(ctx context.Context, orgID uuid.UUID, address string, makeDefault bool, providerRef string) (Address, error) {
	var a Address
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		if makeDefault {
			const clear = `UPDATE qerds_addresses SET is_default = false WHERE organization_id = $1 AND is_default`
			if _, err := q.Exec(ctx, clear, orgID); err != nil {
				return fmt.Errorf("qerds: clear default address org %s: %w", orgID, err)
			}
		}

		var ref *string
		if providerRef != "" {
			ref = &providerRef
		}
		const insert = `INSERT INTO qerds_addresses (organization_id, address, is_default, provider_ref)
			VALUES ($1, $2, $3, $4) RETURNING ` + addressColumns
		var err error
		a, err = scanAddress(q.QueryRow(ctx, insert, orgID, address, makeDefault, ref))
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return ErrAddressTaken
		}
		if err != nil {
			return fmt.Errorf("qerds: provision address org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.QerdsAddressProvisioned,
			audit.Target{Type: audit.TargetQerdsAddress, ID: a.ID.String(), OrgID: &orgID},
			audit.Created(map[string]any{"address": address, "isDefault": makeDefault}))
	})
	return a, err
}

// SetDefaultAddress promotes an existing address to the organization's default
// (sending) address, clearing any previous default so the partial unique index
// (one default per org) holds. Returns ErrAddressNotFound if the address does
// not belong to the organization. Promoting the current default is a no-op.
func (s *Store) SetDefaultAddress(ctx context.Context, orgID, addressID uuid.UUID) (Address, error) {
	var a Address
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		// Scope the lookup to the org so one org can't promote another's address.
		const owned = `SELECT ` + addressColumns + ` FROM qerds_addresses WHERE id = $1 AND organization_id = $2`
		existing, err := scanAddress(q.QueryRow(ctx, owned, addressID, orgID))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAddressNotFound
		}
		if err != nil {
			return fmt.Errorf("qerds: load address %s: %w", addressID, err)
		}
		if existing.IsDefault {
			a = existing
			return nil
		}

		const clear = `UPDATE qerds_addresses SET is_default = false WHERE organization_id = $1 AND is_default`
		if _, err := q.Exec(ctx, clear, orgID); err != nil {
			return fmt.Errorf("qerds: clear default address org %s: %w", orgID, err)
		}
		const promote = `UPDATE qerds_addresses SET is_default = true WHERE id = $1 RETURNING ` + addressColumns
		a, err = scanAddress(q.QueryRow(ctx, promote, addressID))
		if err != nil {
			return fmt.Errorf("qerds: set default address %s: %w", addressID, err)
		}
		return s.audit.Record(ctx, q, audit.QerdsAddressDefaultChanged,
			audit.Target{Type: audit.TargetQerdsAddress, ID: a.ID.String(), OrgID: &orgID},
			audit.Updated(nil, map[string]any{"address": a.Address, "isDefault": true}))
	})
	return a, err
}

// ListAddresses returns an organization's digital addresses, default first.
func (s *Store) ListAddresses(ctx context.Context, orgID uuid.UUID) ([]Address, error) {
	const query = `SELECT ` + addressColumns + ` FROM qerds_addresses WHERE organization_id = $1 ORDER BY is_default DESC, created_at`
	rows, err := s.db.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("qerds: list addresses org %s: %w", orgID, err)
	}
	defer rows.Close()

	addresses := []Address{}
	for rows.Next() {
		a, err := scanAddress(rows)
		if err != nil {
			return nil, fmt.Errorf("qerds: list addresses scan: %w", err)
		}
		addresses = append(addresses, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("qerds: list addresses rows: %w", err)
	}
	return addresses, nil
}

// OrgByAddress resolves the organization that owns a digital address. Used by
// the inbound webhook, which is keyed on recipient address, not org slug.
func (s *Store) OrgByAddress(ctx context.Context, address string) (uuid.UUID, error) {
	const query = `SELECT organization_id FROM qerds_addresses WHERE address = $1`
	var orgID uuid.UUID
	err := s.db.QueryRow(ctx, query, address).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrAddressNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("qerds: org by address %q: %w", address, err)
	}
	return orgID, nil
}

// DefaultAddress returns the organization's default sending address, or
// ErrNoSenderAddress if none is provisioned.
func (s *Store) DefaultAddress(ctx context.Context, orgID uuid.UUID) (Address, error) {
	const query = `SELECT ` + addressColumns + ` FROM qerds_addresses WHERE organization_id = $1 AND is_default`
	a, err := scanAddress(s.db.QueryRow(ctx, query, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Address{}, ErrNoSenderAddress
	}
	if err != nil {
		return Address{}, fmt.Errorf("qerds: default address org %s: %w", orgID, err)
	}
	return a, nil
}
