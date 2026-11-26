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
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/lifecycle"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/walletunit"
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

	// heldSourceBootstrap is attestation.HeldSourceBootstrap: the EBWOID a wallet opens with.
	heldSourceBootstrap = "bootstrap"
)

const orgColumns = `id, name, slug, kvk_number, euid, digital_address, status, bootstrapped_at, test_mode`

// statusAudit maps an org status change to its audit action.
var statusAudit = map[string]string{
	organization.StatusSuspended: audit.WalletSuspended,
	organization.StatusActive:    audit.WalletResumed,
	organization.StatusRevoked:   audit.WalletRevoked,
}

// Store is the pgx-backed persistence for wallet registration and the
// representation (mandate) list. Registration writes across the organizations,
// memberships, qerds_addresses and wallet_representations tables in one tx.
type Store struct {
	db    database.DB
	audit audit.Recorder
	units unitProvisioner
}

// unitProvisioner creates the org's wallet unit inside the registration tx.
type unitProvisioner interface {
	Provision(ctx context.Context, q database.Querier, orgID uuid.UUID) (walletunit.Unit, error)
	RevokeIn(ctx context.Context, q database.Querier, orgID uuid.UUID, reason string) (walletunit.Unit, error)
}

func NewStore(db database.DB, recorder audit.Recorder, units unitProvisioner) *Store {
	return &Store{db: db, audit: recorder, units: units}
}

func scanOrg(row interface{ Scan(...any) error }, org *organization.Organization) error {
	return row.Scan(&org.ID, &org.Name, &org.Slug, &org.KVKNumber, &org.EUID, &org.DigitalAddress, &org.Status, &org.BootstrappedAt, &org.TestMode)
}

// RegisterOrganization creates the organization/business wallet atomically from a
// KVK attestation: the org (identity + slug-based digital address + active status),
// its default (sending) QERDS address, KVK saved as a recipient in the address book
// (kvkContactAddress, the address it delivers the attestation from), the requester
// as first owner (admin membership), the representation list (with the
// requester's own representation claimed), and the held-index row of the EBWOID
// already in the org's holder engine. One tx, following the AcceptInvitation idiom.
func (s *Store) RegisterOrganization(ctx context.Context, reg Registration) (organization.Organization, error) {
	var org organization.Organization
	att := reg.Attestation
	requestorUserID := reg.RequestorUserID
	kvkContactAddress := reg.KVKContactAddress
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		// A registered org starts in test mode; a platform admin makes it live.
		const insOrg = `INSERT INTO organizations (id, name, slug, kvk_number, euid, digital_address, test_mode)
			VALUES ($1, $2, $3, $4, $5, $6, true) RETURNING ` + orgColumns
		err := scanOrg(q.QueryRow(ctx, insOrg, reg.OrgID, att.LegalName, reg.Slug, att.KVKNumber, att.EUID, reg.DigitalAddress), &org)
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
			audit.Created(map[string]any{
				"name": org.Name, "slug": org.Slug, "kvkNumber": org.KVKNumber,
				"testMode": org.TestMode,
			})); err != nil {
			return err
		}

		if err := s.recordEBWOID(ctx, q, org.ID, reg.EBWOID); err != nil {
			return err
		}

		const insAddr = `INSERT INTO qerds_addresses (organization_id, address, is_default) VALUES ($1, $2, true)`
		if _, err := q.Exec(ctx, insAddr, org.ID, reg.DigitalAddress); err != nil {
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

		// The registration disclosure identifies the owner the way an accepted
		// invitation does; a new org has no re-identification policy yet.
		var verifiedAt, dateOfBirth *time.Time
		if reg.Requester.Disclosed {
			now := time.Now()
			verifiedAt = &now
			if dob, err := time.Parse(dobLayout, reg.Requester.DateOfBirth); err == nil {
				dateOfBirth = &dob
			}
		}
		const insMember = `INSERT INTO memberships (organization_id, user_id, role, identity_verified_at, date_of_birth) VALUES ($1, $2, $3, $4, $5)`
		if _, err := q.Exec(ctx, insMember, org.ID, requestorUserID, organization.RoleAdmin, verifiedAt, dateOfBirth); err != nil {
			return fmt.Errorf("wallet: add owner membership: %w", err)
		}

		if err := insertRepresentations(ctx, q, org.ID, requestorUserID, att); err != nil {
			return err
		}

		if _, err := s.units.Provision(ctx, q, org.ID); err != nil {
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

// recordEBWOID indexes the org's EBWOID as a held credential and starts its trail.
func (s *Store) recordEBWOID(ctx context.Context, q database.Querier, orgID uuid.UUID, held registryprovider.HeldEBWOID) error {
	const insHeld = `INSERT INTO held_attestations (organization_id, credential_ref, vct, issuer, source)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`
	var heldID uuid.UUID
	if err := q.QueryRow(ctx, insHeld, orgID, held.Ref, registryprovider.EBWOIDVCT, held.Issuer, heldSourceBootstrap).Scan(&heldID); err != nil {
		return fmt.Errorf("wallet: record EBWOID: %w", err)
	}
	return s.audit.Record(ctx, q, audit.AttestationHeldReceived,
		audit.Target{Type: audit.TargetHeldAttestation, ID: heldID.String(), OrgID: &orgID},
		audit.Created(map[string]any{"vct": registryprovider.EBWOIDVCT, "source": heldSourceBootstrap, "issuer": held.Issuer}))
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
// A move lifecycle does not allow, such as suspending a revoked wallet, is
// refused with lifecycle.ErrTransition.
func (s *Store) SetStatus(ctx context.Context, orgID uuid.UUID, status string) (organization.Organization, error) {
	var org organization.Organization
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		var err error
		org, err = s.setStatusIn(ctx, q, orgID, status)
		return err
	})
	return org, err
}

func (s *Store) setStatusIn(ctx context.Context, q database.Querier, orgID uuid.UUID, status string) (organization.Organization, error) {
	// Locked, so the check and the write see the same status.
	var before string
	err := q.QueryRow(ctx, `SELECT status FROM organizations WHERE id = $1 FOR UPDATE`, orgID).Scan(&before)
	if errors.Is(err, pgx.ErrNoRows) {
		return organization.Organization{}, organization.ErrNotFound
	}
	if err != nil {
		return organization.Organization{}, fmt.Errorf("wallet: read status org %s: %w", orgID, err)
	}
	if err := lifecycle.Transition(before, status); err != nil {
		return organization.Organization{}, err
	}
	var org organization.Organization
	const update = `UPDATE organizations SET status = $1, updated_at = now() WHERE id = $2 RETURNING ` + orgColumns
	if err := scanOrg(q.QueryRow(ctx, update, status, orgID), &org); err != nil {
		return organization.Organization{}, fmt.Errorf("wallet: set status org %s: %w", orgID, err)
	}
	return org, s.audit.Record(ctx, q, statusAudit[status],
		audit.Target{Type: audit.TargetOrganization, ID: org.ID.String(), OrgID: &orgID},
		audit.Updated(map[string]any{"status": before}, map[string]any{"status": status}))
}

// Cease revokes the wallet of an org whose activity ended, and its wallet unit with
// ReasonCessation, in one tx. What is already revoked is left as it is, so it can
// be called again.
func (s *Store) Cease(ctx context.Context, orgID uuid.UUID) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		if _, err := s.setStatusIn(ctx, q, orgID, organization.StatusRevoked); err != nil && !errors.Is(err, lifecycle.ErrTransition) {
			return err
		}
		if _, err := s.units.RevokeIn(ctx, q, orgID, walletunit.ReasonCessation); err != nil && !errors.Is(err, walletunit.ErrNoActiveUnit) {
			return err
		}
		return nil
	})
}

// SuspendForProvider revokes every live wallet unit and suspends every active
// wallet, in one tx, because we as provider are off the trusted list (Art
// 6(2)(f)), and returns the orgs it changed. Each wallet it suspends is
// remembered, so ResumeForProvider brings back exactly these and not a wallet an
// admin suspended.
func (s *Store) SuspendForProvider(ctx context.Context) ([]uuid.UUID, error) {
	var changed []uuid.UUID
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		changed = nil
		rows, err := q.Query(ctx, `SELECT id, status FROM organizations WHERE status IN ($1, $2) ORDER BY created_at FOR UPDATE`,
			organization.StatusActive, organization.StatusSuspended)
		if err != nil {
			return fmt.Errorf("wallet: live orgs: %w", err)
		}
		type liveOrg struct {
			ID     uuid.UUID
			Status string
		}
		live, err := pgx.CollectRows(rows, pgx.RowToStructByPos[liveOrg])
		if err != nil {
			return fmt.Errorf("wallet: live orgs rows: %w", err)
		}
		for _, org := range live {
			_, err := s.units.RevokeIn(ctx, q, org.ID, walletunit.ReasonProviderDelisted)
			revoked := err == nil
			if err != nil && !errors.Is(err, walletunit.ErrNoActiveUnit) {
				return err
			}
			if org.Status == organization.StatusActive {
				if err := s.suspendForProviderIn(ctx, q, org.ID); err != nil {
					return err
				}
			}
			if revoked || org.Status == organization.StatusActive {
				changed = append(changed, org.ID)
			}
		}
		return nil
	})
	return changed, err
}

func (s *Store) suspendForProviderIn(ctx context.Context, q database.Querier, orgID uuid.UUID) error {
	if _, err := s.setStatusIn(ctx, q, orgID, organization.StatusSuspended); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `INSERT INTO provider_suspended_wallets (organization_id) VALUES ($1)
		ON CONFLICT DO NOTHING`, orgID); err != nil {
		return fmt.Errorf("wallet: remember provider suspension org %s: %w", orgID, err)
	}
	return nil
}

// ResumeForProvider makes the wallets SuspendForProvider suspended active again,
// each with a new unit in place of the one it revoked, in one tx, and returns
// those orgs. One revoked since then stays revoked.
func (s *Store) ResumeForProvider(ctx context.Context) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		var err error
		ids, err = orgIDs(ctx, q, `SELECT o.id FROM provider_suspended_wallets p
			JOIN organizations o ON o.id = p.organization_id
			WHERE o.status = $1 ORDER BY o.created_at FOR UPDATE OF o`, organization.StatusSuspended)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := s.setStatusIn(ctx, q, id, organization.StatusActive); err != nil {
				return err
			}
			if err := s.replaceDelistedUnit(ctx, q, id); err != nil {
				return err
			}
		}
		if _, err := q.Exec(ctx, `DELETE FROM provider_suspended_wallets`); err != nil {
			return fmt.Errorf("wallet: forget provider suspensions: %w", err)
		}
		return nil
	})
	return ids, err
}

// replaceDelistedUnit gives the org a new unit when its latest was revoked for
// ReasonProviderDelisted; an org that never had a unit gets its first on issue.
func (s *Store) replaceDelistedUnit(ctx context.Context, q database.Querier, orgID uuid.UUID) error {
	var delisted bool
	err := q.QueryRow(ctx, `SELECT status = 'revoked' AND revoked_reason = $2 FROM wallet_units
		WHERE organization_id = $1 ORDER BY (status = 'active') DESC, created_at DESC LIMIT 1`,
		orgID, walletunit.ReasonProviderDelisted).Scan(&delisted)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !delisted) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("wallet: latest unit org %s: %w", orgID, err)
	}
	_, err = s.units.Provision(ctx, q, orgID)
	return err
}

func orgIDs(ctx context.Context, q database.Querier, query string, args ...any) ([]uuid.UUID, error) {
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("wallet: list orgs: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, fmt.Errorf("wallet: list orgs rows: %w", err)
	}
	return ids, nil
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
