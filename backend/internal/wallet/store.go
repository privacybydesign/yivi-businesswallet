package wallet

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
)

const dobLayout = "2006-01-02"

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

const uniqueViolation = "23505"

// statusAudit maps a lifecycle status change to its audit action.
var statusAudit = map[string]string{
	StatusSuspended: audit.WalletSuspended,
	StatusRevoked:   audit.WalletRevoked,
}

// Store is the pgx-backed persistence for wallet instances and their
// representation (mandate) list.
type Store struct {
	db    database.DB
	audit audit.Recorder
}

func NewStore(db database.DB, recorder audit.Recorder) *Store {
	return &Store{db: db, audit: recorder}
}

type rowScanner interface {
	Scan(dest ...any) error
}

const instanceColumns = `id, status, kvk_number, digital_address, organization_id, legal_name, euid, reject_reason, bootstrapped_at, created_at, updated_at`

func scanInstance(row rowScanner) (Instance, error) {
	var (
		in           Instance
		orgID        *uuid.UUID
		legalName    *string
		euid         *string
		rejectReason *string
	)
	if err := row.Scan(&in.ID, &in.Status, &in.KVKNumber, &in.DigitalAddress, &orgID, &legalName, &euid, &rejectReason, &in.BootstrappedAt, &in.CreatedAt, &in.UpdatedAt); err != nil {
		return Instance{}, err
	}
	in.OrganizationID = orgID
	if legalName != nil {
		in.LegalName = *legalName
	}
	if euid != nil {
		in.EUID = *euid
	}
	if rejectReason != nil {
		in.RejectReason = *rejectReason
	}
	return in, nil
}

// CreateInstance opens a wallet in the provisioning state with its own digital
// address, before the organization identity is known. The partial unique index
// on (requestor_user_id, kvk_number) for in-flight statuses surfaces a duplicate
// as ErrRegistrationInProgress.
func (s *Store) CreateInstance(ctx context.Context, requestorUserID uuid.UUID, kvkNumber, digitalAddress string) (Instance, error) {
	var in Instance
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO wallet_instances (status, requestor_user_id, kvk_number, digital_address)
			VALUES ($1, $2, $3, $4) RETURNING ` + instanceColumns
		var err error
		in, err = scanInstance(q.QueryRow(ctx, insert, StatusProvisioning, requestorUserID, kvkNumber, digitalAddress))
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return ErrRegistrationInProgress
		}
		if err != nil {
			return fmt.Errorf("wallet: create instance: %w", err)
		}
		return s.audit.Record(ctx, q, audit.WalletOpened,
			audit.Target{Type: audit.TargetWalletInstance, ID: in.ID.String()},
			audit.Created(map[string]any{"kvkNumber": kvkNumber, "digitalAddress": digitalAddress}))
	})
	return in, err
}

// MarkRequested moves a provisioning instance to awaiting_attestation once the
// outbound KVK request has been sent.
//
// TODO(wallet-bootstrap): also persist request_message_id once the outbound
// {PID, KVK} QERDS message is created through the qerds slice (see §6.1).
func (s *Store) MarkRequested(ctx context.Context, id uuid.UUID) error {
	const update = `UPDATE wallet_instances SET status = $1, updated_at = now() WHERE id = $2 AND status = $3`
	tag, err := s.db.Exec(ctx, update, StatusAwaitingAttestation, id, StatusProvisioning)
	if err != nil {
		return fmt.Errorf("wallet: mark requested %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrInstanceNotFound
	}
	return nil
}

// GetInstanceByID loads an instance by its id (central poll path).
func (s *Store) GetInstanceByID(ctx context.Context, id uuid.UUID) (Instance, error) {
	const query = `SELECT ` + instanceColumns + ` FROM wallet_instances WHERE id = $1`
	in, err := scanInstance(s.db.QueryRow(ctx, query, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Instance{}, ErrInstanceNotFound
	}
	if err != nil {
		return Instance{}, fmt.Errorf("wallet: get instance %s: %w", id, err)
	}
	return in, nil
}

// GetInstanceByOrg loads the active instance backing an organization.
func (s *Store) GetInstanceByOrg(ctx context.Context, orgID uuid.UUID) (Instance, error) {
	const query = `SELECT ` + instanceColumns + ` FROM wallet_instances WHERE organization_id = $1`
	in, err := scanInstance(s.db.QueryRow(ctx, query, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Instance{}, ErrInstanceNotFound
	}
	if err != nil {
		return Instance{}, fmt.Errorf("wallet: get instance for org %s: %w", orgID, err)
	}
	return in, nil
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

// SetStatus transitions an instance (suspend/revoke) and audits the change.
func (s *Store) SetStatus(ctx context.Context, orgID uuid.UUID, status string) (Instance, error) {
	var in Instance
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE wallet_instances SET status = $1, updated_at = now() WHERE organization_id = $2 RETURNING ` + instanceColumns
		var err error
		in, err = scanInstance(q.QueryRow(ctx, update, status, orgID))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInstanceNotFound
		}
		if err != nil {
			return fmt.Errorf("wallet: set status org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, statusAudit[status],
			audit.Target{Type: audit.TargetWalletInstance, ID: in.ID.String(), OrgID: &orgID},
			audit.Updated(nil, map[string]any{"status": status}))
	})
	return in, err
}

// ActivateFromAttestation applies KVK's attestation atomically: it creates the
// organization (with a unique slug), provisions its default digital address from
// the wallet's provisioning address, records every representation, makes the
// requester the first owner (admin membership + claimed representation) and marks
// the instance active — all in one transaction, following the AcceptInvitation
// idiom (one store method, one tx, raw SQL per table). See §6.2.
func (s *Store) ActivateFromAttestation(ctx context.Context, instanceID uuid.UUID, att registryprovider.RegistrationAttestation) (Instance, error) {
	var out Instance
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		// Lock the instance and read what we need; guard against double activation.
		var (
			requestorUserID *uuid.UUID
			digitalAddress  string
			status          string
		)
		const load = `SELECT requestor_user_id, digital_address, status FROM wallet_instances WHERE id = $1 FOR UPDATE`
		err := q.QueryRow(ctx, load, instanceID).Scan(&requestorUserID, &digitalAddress, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInstanceNotFound
		}
		if err != nil {
			return fmt.Errorf("wallet: load instance %s: %w", instanceID, err)
		}
		if status != StatusAwaitingAttestation && status != StatusProvisioning {
			return fmt.Errorf("wallet: instance %s is not awaiting attestation (status %q)", instanceID, status)
		}

		// Organization identity (Art 8) with a URL-safe, collision-free slug.
		slug, err := uniqueSlug(ctx, q, att.LegalName)
		if err != nil {
			return err
		}
		var orgID uuid.UUID
		const insOrg = `INSERT INTO organizations (name, slug) VALUES ($1, $2) RETURNING id`
		if err := q.QueryRow(ctx, insOrg, att.LegalName, slug).Scan(&orgID); err != nil {
			return fmt.Errorf("wallet: create organization: %w", err)
		}
		if err := s.audit.Record(ctx, q, audit.OrganizationCreated,
			audit.Target{Type: audit.TargetOrganization, ID: orgID.String(), OrgID: &orgID},
			audit.Created(map[string]any{"name": att.LegalName, "slug": slug})); err != nil {
			return err
		}

		// The wallet's provisioning address becomes the org's default QERDS address.
		const insAddr = `INSERT INTO qerds_addresses (organization_id, address, is_default) VALUES ($1, $2, true)`
		if _, err := q.Exec(ctx, insAddr, orgID, digitalAddress); err != nil {
			return fmt.Errorf("wallet: provision address: %w", err)
		}

		// First owner: the requester becomes an admin member.
		if requestorUserID != nil {
			const insMember = `INSERT INTO memberships (organization_id, user_id, role) VALUES ($1, $2, $3)`
			if _, err := q.Exec(ctx, insMember, orgID, *requestorUserID, organization.RoleAdmin); err != nil {
				return fmt.Errorf("wallet: add owner membership: %w", err)
			}
		}

		// The mandate list. The requester's own representation is marked claimed.
		const insRep = `INSERT INTO wallet_representations
			(wallet_instance_id, organization_id, kind, given_names, family_name, date_of_birth, authority, claimed_by_user_id, claimed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
		ownerClaimed := att.RequesterIsRepresentative && requestorUserID != nil
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
				claimedBy = requestorUserID
				claimedAt = &now
			}
			if _, err := q.Exec(ctx, insRep, instanceID, orgID, rep.Kind, rep.GivenNames, rep.FamilyName, dob, rep.Authority, claimedBy, claimedAt); err != nil {
				return fmt.Errorf("wallet: insert representation: %w", err)
			}
		}

		// Activate: link the org and populate the verified identity.
		const upd = `UPDATE wallet_instances
			SET status = $2, organization_id = $3, legal_name = $4, euid = $5, bootstrapped_at = now(), updated_at = now()
			WHERE id = $1 RETURNING ` + instanceColumns
		out, err = scanInstance(q.QueryRow(ctx, upd, instanceID, StatusActive, orgID, att.LegalName, att.EUID))
		if err != nil {
			return fmt.Errorf("wallet: activate instance %s: %w", instanceID, err)
		}

		return s.audit.Record(ctx, q, audit.WalletBootstrapped,
			audit.Target{Type: audit.TargetWalletInstance, ID: instanceID.String(), OrgID: &orgID},
			audit.Created(map[string]any{
				"legalName":       att.LegalName,
				"euid":            att.EUID,
				"kvkNumber":       att.KVKNumber,
				"slug":            slug,
				"representatives": len(att.Representatives),
			}))
	})
	return out, err
}

// RejectInstance marks an awaiting instance rejected (e.g. the requester was not
// a listed representative). No organization is created, so there is nothing to
// audit under an org scope; the reason is retained on the instance row.
func (s *Store) RejectInstance(ctx context.Context, instanceID uuid.UUID, reason string) (Instance, error) {
	const upd = `UPDATE wallet_instances SET status = $2, reject_reason = $3, updated_at = now()
		WHERE id = $1 AND status = $4 RETURNING ` + instanceColumns
	out, err := scanInstance(s.db.QueryRow(ctx, upd, instanceID, StatusRejected, reason, StatusAwaitingAttestation))
	if errors.Is(err, pgx.ErrNoRows) {
		return Instance{}, ErrInstanceNotFound
	}
	if err != nil {
		return Instance{}, fmt.Errorf("wallet: reject instance %s: %w", instanceID, err)
	}
	return out, nil
}

// ClaimRepresentation binds a person to an unclaimed representation and grants
// the corresponding membership. See §6.3.
//
// TODO(wallet-bootstrap): match the claimant's OpenID4VP-disclosed identity
// (name+DOB) to the representation, then create the membership in one tx. Blocked
// on the OpenID4VP swap (the claimant's verified identity is not yet available).
func (s *Store) ClaimRepresentation(_ context.Context, _, _, _ uuid.UUID) error {
	return ErrNotImplemented
}

// uniqueSlug derives a URL-safe slug from name and appends -2, -3, … until it is
// free. It reads the taken set inside the caller's tx so the chosen slug holds
// until commit; the UNIQUE constraint remains the backstop against a race.
func uniqueSlug(ctx context.Context, q database.Querier, name string) (string, error) {
	base := slugify(name)
	if base == "" {
		base = "org"
	}
	if organization.ValidateSlug(base) != nil {
		base = "org-" + base
	}

	rows, err := q.Query(ctx, `SELECT slug FROM organizations WHERE slug = $1 OR slug LIKE $1 || '-%'`, base)
	if err != nil {
		return "", fmt.Errorf("wallet: slug lookup: %w", err)
	}
	defer rows.Close()
	taken := map[string]struct{}{}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return "", fmt.Errorf("wallet: slug scan: %w", err)
		}
		taken[slug] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("wallet: slug rows: %w", err)
	}

	if _, ok := taken[base]; !ok {
		return base, nil
	}
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s-%d", base, i)
		if _, ok := taken[cand]; !ok {
			return cand, nil
		}
	}
}

func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = nonSlugChars.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}
