package wallet

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
)

const (
	dobLayout       = "2006-01-02"
	uniqueViolation = "23505"

	// Postgres auto-names UNIQUE column constraints <table>_<column>_key.
	orgKvkConstraint     = "organizations_kvk_number_key"
	orgSlugConstraint    = "organizations_slug_key"
	orgAddressConstraint = "organizations_digital_address_key"

	// kvkContactName labels the KVK-derived recipient saved to the new org's
	// address book at bootstrap.
	kvkContactName = "KVK"
)

const orgColumns = `id, name, slug, kvk_number, euid, digital_address, status, bootstrapped_at`

// statusAudit maps an org status change to its audit action.
var statusAudit = map[string]string{
	organization.StatusSuspended: audit.WalletSuspended,
	organization.StatusRevoked:   audit.WalletRevoked,
}

// Store is the pgx-backed persistence for wallet registration and the
// representation (mandate) list. Registration writes across the organizations,
// memberships, qerds_addresses and wallet_representations tables in one tx.
type Store struct {
	db    database.DB
	audit audit.Recorder
}

func NewStore(db database.DB, recorder audit.Recorder) *Store {
	return &Store{db: db, audit: recorder}
}

func scanOrg(row interface{ Scan(...any) error }, org *organization.Organization) error {
	return row.Scan(&org.ID, &org.Name, &org.Slug, &org.KVKNumber, &org.EUID, &org.DigitalAddress, &org.Status, &org.BootstrappedAt)
}

// RegisterOrganization creates the organization/business wallet atomically from a
// KVK attestation: the org (identity + slug-based digital address + active status),
// its default (sending) QERDS address, KVK saved as a recipient in the address book
// (kvkContactAddress, the address it delivers the attestation from), the requester
// as first owner (admin membership), and the representation list (with the
// requester's own representation claimed). One tx, following the AcceptInvitation
// idiom.
func (s *Store) RegisterOrganization(ctx context.Context, requestorUserID uuid.UUID, slug, digitalAddress, kvkContactAddress string, att registryprovider.RegistrationAttestation) (organization.Organization, error) {
	var org organization.Organization
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const insOrg = `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
			VALUES ($1, $2, $3, $4, $5) RETURNING ` + orgColumns
		err := scanOrg(q.QueryRow(ctx, insOrg, att.LegalName, slug, att.KVKNumber, att.EUID, digitalAddress), &org)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			switch pgErr.ConstraintName {
			case orgKvkConstraint:
				return ErrAlreadyRegistered
			case orgSlugConstraint, orgAddressConstraint:
				// digital_address is derived from the slug — a collision here
				// means the slug is taken, not that the KVK is already registered.
				return ErrSlugTaken
			}
		}
		if err != nil {
			return fmt.Errorf("wallet: create organization: %w", err)
		}

		if err := s.audit.Record(ctx, q, audit.OrganizationCreated,
			audit.Target{Type: audit.TargetOrganization, ID: org.ID.String(), OrgID: &org.ID},
			audit.Created(map[string]any{"name": org.Name, "slug": org.Slug, "kvkNumber": org.KVKNumber})); err != nil {
			return err
		}

		const insAddr = `INSERT INTO qerds_addresses (organization_id, address, is_default) VALUES ($1, $2, true)`
		if _, err := q.Exec(ctx, insAddr, org.ID, digitalAddress); err != nil {
			return fmt.Errorf("wallet: provision address: %w", err)
		}

		// Save KVK as a recipient in the org's address book (its delivery address).
		const insContact = `INSERT INTO qerds_contacts (organization_id, name, address) VALUES ($1, $2, $3) RETURNING id`
		var contactID uuid.UUID
		if err := q.QueryRow(ctx, insContact, org.ID, kvkContactName, kvkContactAddress).Scan(&contactID); err != nil {
			return fmt.Errorf("wallet: save kvk contact: %w", err)
		}
		if err := s.audit.Record(ctx, q, audit.QerdsContactAdded,
			audit.Target{Type: audit.TargetQerdsContact, ID: contactID.String(), OrgID: &org.ID},
			audit.Created(map[string]any{"name": kvkContactName, "address": kvkContactAddress})); err != nil {
			return err
		}

		const insMember = `INSERT INTO memberships (organization_id, user_id, role) VALUES ($1, $2, $3)`
		if _, err := q.Exec(ctx, insMember, org.ID, requestorUserID, organization.RoleAdmin); err != nil {
			return fmt.Errorf("wallet: add owner membership: %w", err)
		}

		if err := insertRepresentations(ctx, q, org.ID, requestorUserID, att); err != nil {
			return err
		}

		return s.audit.Record(ctx, q, audit.WalletBootstrapped,
			audit.Target{Type: audit.TargetOrganization, ID: org.ID.String(), OrgID: &org.ID},
			audit.Created(map[string]any{
				"legalName":       org.Name,
				"euid":            org.EUID,
				"kvkNumber":       org.KVKNumber,
				"slug":            org.Slug,
				"representatives": len(att.Representatives),
			}))
	})
	return org, err
}

func insertRepresentations(ctx context.Context, q database.Querier, orgID, requestorUserID uuid.UUID, att registryprovider.RegistrationAttestation) error {
	const insRep = `INSERT INTO wallet_representations
		(organization_id, kind, given_names, family_name, date_of_birth, authority, claimed_by_user_id, claimed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	ownerClaimed := att.RequesterIsRepresentative
	for i, rep := range att.Representatives {
		var (
			dob       *time.Time
			claimedBy *uuid.UUID
			claimedAt *time.Time
		)
		if rep.DateOfBirth != "" {
			if t, perr := time.Parse(dobLayout, rep.DateOfBirth); perr == nil {
				dob = &t
			}
		}
		if ownerClaimed && i == att.RequesterRepresentativeIndex {
			now := time.Now().UTC()
			uid := requestorUserID
			claimedBy = &uid
			claimedAt = &now
		}
		if _, err := q.Exec(ctx, insRep, orgID, rep.Kind, rep.GivenNames, rep.FamilyName, dob, rep.Authority, claimedBy, claimedAt); err != nil {
			return fmt.Errorf("wallet: insert representation: %w", err)
		}
	}
	return nil
}

// SetStatus transitions an org/wallet (suspend/revoke) and audits the change.
func (s *Store) SetStatus(ctx context.Context, orgID uuid.UUID, status string) (organization.Organization, error) {
	var org organization.Organization
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE organizations SET status = $1, updated_at = now() WHERE id = $2 RETURNING ` + orgColumns
		err := scanOrg(q.QueryRow(ctx, update, status, orgID), &org)
		if errors.Is(err, pgx.ErrNoRows) {
			return organization.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("wallet: set status org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, statusAudit[status],
			audit.Target{Type: audit.TargetOrganization, ID: org.ID.String(), OrgID: &orgID},
			audit.Updated(nil, map[string]any{"status": status}))
	})
	return org, err
}

// ListRepresentations returns the (non-revoked) mandate list for an org.
func (s *Store) ListRepresentations(ctx context.Context, orgID uuid.UUID) ([]Representation, error) {
	const query = `SELECT id, kind, given_names, family_name, authority, claimed_by_user_id, claimed_at, created_at
		FROM wallet_representations WHERE organization_id = $1 AND revoked_at IS NULL
		ORDER BY family_name, given_names`
	rows, err := s.db.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("wallet: list representations org %s: %w", orgID, err)
	}
	defer rows.Close()

	reps := []Representation{}
	for rows.Next() {
		var (
			r         Representation
			claimedBy *uuid.UUID
		)
		if err := rows.Scan(&r.ID, &r.Kind, &r.GivenNames, &r.FamilyName, &r.Authority, &claimedBy, &r.ClaimedAt, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("wallet: list representations scan: %w", err)
		}
		r.Claimed = claimedBy != nil
		reps = append(reps, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("wallet: list representations rows: %w", err)
	}
	return reps, nil
}

// ClaimRepresentation binds a person to an unclaimed representation and grants the
// corresponding membership. See §6.3.
//
// TODO(wallet-bootstrap): match the claimant's OpenID4VP identity (name+DOB) to
// the representation, then create the membership in one tx.
func (s *Store) ClaimRepresentation(_ context.Context, _, _, _ uuid.UUID) error {
	return ErrNotImplemented
}
