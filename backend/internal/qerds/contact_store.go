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

const contactColumns = `id, organization_id, name, address, legal_name, kvk_number, euid, created_at, updated_at`

func scanContact(row rowScanner) (Contact, error) {
	var c Contact
	if err := row.Scan(&c.ID, &c.OrganizationID, &c.Name, &c.Address, &c.LegalName, &c.KVKNumber, &c.EUID, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return Contact{}, err
	}
	return c, nil
}

// CreateContact saves a recipient to an organization's address book. The org
// fields (legalName/kvkNumber/euid) are optional and may be nil.
func (s *Store) CreateContact(ctx context.Context, orgID uuid.UUID, in Contact) (Contact, error) {
	var c Contact
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO qerds_contacts (organization_id, name, address, legal_name, kvk_number, euid)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING ` + contactColumns
		var err error
		c, err = scanContact(q.QueryRow(ctx, insert, orgID, in.Name, in.Address, in.LegalName, in.KVKNumber, in.EUID))
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return ErrContactAddressTaken
		}
		if err != nil {
			return fmt.Errorf("qerds: create contact org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.QerdsContactAdded,
			audit.Target{Type: audit.TargetQerdsContact, ID: c.ID.String(), OrgID: &orgID},
			audit.Created(map[string]any{"name": in.Name, "address": in.Address}))
	})
	return c, err
}

// ListContacts returns an organization's saved recipients, alphabetical by name.
func (s *Store) ListContacts(ctx context.Context, orgID uuid.UUID) ([]Contact, error) {
	const query = `SELECT ` + contactColumns + ` FROM qerds_contacts WHERE organization_id = $1 ORDER BY name, address`
	rows, err := s.db.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("qerds: list contacts org %s: %w", orgID, err)
	}
	defer rows.Close()

	contacts := []Contact{}
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, fmt.Errorf("qerds: list contacts scan: %w", err)
		}
		contacts = append(contacts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("qerds: list contacts rows: %w", err)
	}
	return contacts, nil
}

// DeleteContact removes a saved recipient from an organization's address book.
func (s *Store) DeleteContact(ctx context.Context, orgID, id uuid.UUID) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const del = `DELETE FROM qerds_contacts WHERE organization_id = $1 AND id = $2 RETURNING name, address`
		var name, address string
		err := q.QueryRow(ctx, del, orgID, id).Scan(&name, &address)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrContactNotFound
		}
		if err != nil {
			return fmt.Errorf("qerds: delete contact %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.QerdsContactDeleted,
			audit.Target{Type: audit.TargetQerdsContact, ID: id.String(), OrgID: &orgID},
			audit.Deleted(map[string]any{"name": name, "address": address}))
	})
}
