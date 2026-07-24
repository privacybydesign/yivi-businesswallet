package attestation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

const issuedColumns = `id, organization_id, template_id, schema_vct, recipient_kind,
	recipient_user_id, recipient_ref, attributes, qualified, status, delivery, issuance_id,
	issued_by_user_id, claimed_at, expires_at, revoked_at, cancelled_at, created_at, updated_at`

func scanIssued(row rowScanner) (Issued, error) {
	var (
		i        Issued
		attrsRaw []byte
	)
	if err := row.Scan(
		&i.ID, &i.OrganizationID, &i.TemplateID, &i.SchemaVCT, &i.RecipientKind,
		&i.RecipientUserID, &i.RecipientRef, &attrsRaw, &i.Qualified, &i.Status, &i.Delivery, &i.IssuanceID,
		&i.IssuedByUserID, &i.ClaimedAt, &i.ExpiresAt, &i.RevokedAt, &i.CancelledAt, &i.CreatedAt, &i.UpdatedAt,
	); err != nil {
		return Issued{}, err
	}
	attrs, err := unmarshalStringMap(attrsRaw)
	if err != nil {
		return Issued{}, err
	}
	i.Attributes = attrs
	return i, nil
}

// ListIssued returns an organization's issuance ledger, newest first.
func (s *Store) ListIssued(ctx context.Context, orgID uuid.UUID) ([]Issued, error) {
	const query = `SELECT ` + issuedColumns + ` FROM issued_attestations WHERE organization_id = $1 ORDER BY created_at DESC`
	rows, err := s.db.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("attestation: list issued org %s: %w", orgID, err)
	}
	defer rows.Close()

	issued := []Issued{}
	for rows.Next() {
		i, err := scanIssued(rows)
		if err != nil {
			return nil, fmt.Errorf("attestation: list issued scan: %w", err)
		}
		issued = append(issued, i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("attestation: list issued rows: %w", err)
	}
	return issued, nil
}

// GetIssued returns one org-scoped issued attestation.
func (s *Store) GetIssued(ctx context.Context, orgID, id uuid.UUID) (Issued, error) {
	const query = `SELECT ` + issuedColumns + ` FROM issued_attestations WHERE organization_id = $1 AND id = $2`
	i, err := scanIssued(s.db.QueryRow(ctx, query, orgID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Issued{}, ErrIssuedNotFound
	}
	if err != nil {
		return Issued{}, fmt.Errorf("attestation: get issued %s: %w", id, err)
	}
	return i, nil
}

// CreateOffered persists a new offered attestation and audits, in one tx — before
// the issuer offer is created. A later issuer failure marks the row failed; the
// ledger entry is never lost (Art 5(1)(m)).
func (s *Store) CreateOffered(ctx context.Context, orgID uuid.UUID, in IssueInput, detail TemplateDetail, issuedBy *uuid.UUID, expiresAt *time.Time, claimToken, delivery string) (Issued, error) {
	attrs, err := marshalJSON(in.Attributes)
	if err != nil {
		return Issued{}, err
	}

	var out Issued
	err = database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO issued_attestations
			(organization_id, template_id, schema_vct, recipient_kind, recipient_user_id,
			 recipient_ref, attributes, qualified, status, delivery, claim_token, issued_by_user_id, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			RETURNING ` + issuedColumns
		var err error
		out, err = scanIssued(q.QueryRow(ctx, insert,
			orgID, in.TemplateID, detail.SchemaVCT, in.Recipient.Kind, in.Recipient.UserID,
			in.Recipient.Ref, attrs, detail.Qualified, StatusOffered, delivery, claimToken, issuedBy, expiresAt))
		if err != nil {
			return fmt.Errorf("attestation: create offered org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.AttestationIssued,
			audit.Target{Type: audit.TargetIssuedAttestation, ID: out.ID.String(), OrgID: &orgID},
			audit.Created(map[string]any{
				"schemaVct": out.SchemaVCT, "recipient": out.RecipientRef,
				"recipientKind": out.RecipientKind, "qualified": out.Qualified,
			}))
	})
	return out, err
}

// SetOffer records the created offer (issuer correlation id + the wallet offer
// URI and tx_code) on an offered attestation, so the claim page can render it.
func (s *Store) SetOffer(ctx context.Context, orgID, id uuid.UUID, issuanceID, offerURI, txCode string) error {
	const update = `UPDATE issued_attestations
		SET issuance_id = $3, offer_uri = $4, tx_code = $5, updated_at = now()
		WHERE organization_id = $1 AND id = $2`
	if _, err := s.db.Exec(ctx, update, orgID, id, issuanceID, offerURI, txCode); err != nil {
		return fmt.Errorf("attestation: set offer %s: %w", id, err)
	}
	return nil
}

// claimRow is the internal projection the public claim flow needs.
type claimRow struct {
	id             uuid.UUID
	orgID          uuid.UUID
	status         string
	issuanceID     string
	offerURI       string
	txCode         string
	orgName        string
	credentialName string
}

// GetClaim resolves an issued attestation by its opaque claim token, joining the
// issuer org name and a friendly credential name.
func (s *Store) GetClaim(ctx context.Context, token string) (claimRow, error) {
	const query = `SELECT ia.id, ia.organization_id, ia.status, ia.issuance_id, ia.offer_uri, ia.tx_code,
			o.name, COALESCE(t.name, ia.schema_vct)
		FROM issued_attestations ia
		JOIN organizations o ON o.id = ia.organization_id
		LEFT JOIN attestation_templates t ON t.id = ia.template_id
		WHERE ia.claim_token = $1`
	var c claimRow
	err := s.db.QueryRow(ctx, query, token).Scan(
		&c.id, &c.orgID, &c.status, &c.issuanceID, &c.offerURI, &c.txCode, &c.orgName, &c.credentialName,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return claimRow{}, ErrClaimNotFound
	}
	if err != nil {
		return claimRow{}, fmt.Errorf("attestation: get claim: %w", err)
	}
	return c, nil
}

// MarkFailed transitions an offered attestation to failed (issuer offer failed).
func (s *Store) MarkFailed(ctx context.Context, orgID, id uuid.UUID) error {
	const update = `UPDATE issued_attestations SET status = $3, updated_at = now()
		WHERE organization_id = $1 AND id = $2 AND status = $4`
	if _, err := s.db.Exec(ctx, update, orgID, id, StatusFailed, StatusOffered); err != nil {
		return fmt.Errorf("attestation: mark failed %s: %w", id, err)
	}
	return nil
}

// MarkClaimed transitions an offered attestation to claimed and audits, in one tx.
func (s *Store) MarkClaimed(ctx context.Context, orgID, id uuid.UUID) (Issued, error) {
	var out Issued
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE issued_attestations SET status = $3, claimed_at = now(), updated_at = now()
			WHERE organization_id = $1 AND id = $2 AND status = $4
			RETURNING ` + issuedColumns
		var err error
		out, err = scanIssued(q.QueryRow(ctx, update, orgID, id, StatusClaimed, StatusOffered))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrIssuedNotFound
		}
		if err != nil {
			return fmt.Errorf("attestation: mark claimed %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.AttestationClaimed,
			audit.Target{Type: audit.TargetIssuedAttestation, ID: out.ID.String(), OrgID: &orgID},
			audit.Updated(nil, map[string]any{"status": out.Status}))
	})
	return out, err
}

// Revoke transitions a claimed attestation to revoked and audits, in one tx.
// Only already-claimed credentials are revocable — an unclaimed offer is
// withdrawn via Cancel, not revoked. Rows in any other state (offered, revoked,
// cancelled, expired, failed) return ErrNotOfferable.
func (s *Store) Revoke(ctx context.Context, orgID, id uuid.UUID) (Issued, error) {
	var out Issued
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE issued_attestations SET status = $3, revoked_at = now(), updated_at = now()
			WHERE organization_id = $1 AND id = $2 AND status = $4
			RETURNING ` + issuedColumns
		var err error
		out, err = scanIssued(q.QueryRow(ctx, update, orgID, id, StatusRevoked, StatusClaimed))
		if errors.Is(err, pgx.ErrNoRows) {
			// Distinguish "not found" from "not in a revocable state".
			if _, getErr := s.getIssuedTx(ctx, q, orgID, id); errors.Is(getErr, ErrIssuedNotFound) {
				return ErrIssuedNotFound
			}
			return ErrNotOfferable
		}
		if err != nil {
			return fmt.Errorf("attestation: revoke %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.AttestationRevoked,
			audit.Target{Type: audit.TargetIssuedAttestation, ID: out.ID.String(), OrgID: &orgID},
			audit.Updated(nil, map[string]any{"status": out.Status}))
	})
	return out, err
}

// Cancel transitions an unclaimed offer to cancelled and audits, in one tx.
// Only offers (status 'offered') can be cancelled — nothing was ever held, so
// this is not a revocation and never touches the Token Status List. Rows in any
// other state (claimed, revoked, cancelled, expired, failed) return ErrNotOfferable.
func (s *Store) Cancel(ctx context.Context, orgID, id uuid.UUID) (Issued, error) {
	var out Issued
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE issued_attestations SET status = $3, cancelled_at = now(), updated_at = now()
			WHERE organization_id = $1 AND id = $2 AND status = $4
			RETURNING ` + issuedColumns
		var err error
		out, err = scanIssued(q.QueryRow(ctx, update, orgID, id, StatusCancelled, StatusOffered))
		if errors.Is(err, pgx.ErrNoRows) {
			// Distinguish "not found" from "not in a cancellable state".
			if _, getErr := s.getIssuedTx(ctx, q, orgID, id); errors.Is(getErr, ErrIssuedNotFound) {
				return ErrIssuedNotFound
			}
			return ErrNotOfferable
		}
		if err != nil {
			return fmt.Errorf("attestation: cancel %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.AttestationOfferCancelled,
			audit.Target{Type: audit.TargetIssuedAttestation, ID: out.ID.String(), OrgID: &orgID},
			audit.Updated(nil, map[string]any{"status": out.Status}))
	})
	return out, err
}

func (s *Store) getIssuedTx(ctx context.Context, q database.Querier, orgID, id uuid.UUID) (Issued, error) {
	const query = `SELECT ` + issuedColumns + ` FROM issued_attestations WHERE organization_id = $1 AND id = $2`
	i, err := scanIssued(q.QueryRow(ctx, query, orgID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Issued{}, ErrIssuedNotFound
	}
	return i, err
}
