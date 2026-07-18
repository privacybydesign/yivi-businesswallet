package attestation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/privacybydesign/irmago/common/clientmodels"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// HeldCredentialView is the display projection of a held credential: the org's
// index metadata (id, provenance) merged with irmago's clientmodels display
// model (localized name, issuer, attributes, logos) read from the holder engine.
// The Credential shape mirrors what the irmamobile wallet renders, so the
// frontend can render held credentials the same way.
type HeldCredentialView struct {
	HeldID          uuid.UUID                `json:"heldId"`
	Source          string                   `json:"source"`
	ReceivedAt      time.Time                `json:"receivedAt"`
	SourceMessageID *uuid.UUID               `json:"sourceMessageId,omitempty"`
	Credential      *clientmodels.Credential `json:"credential"`
}

// Held-credential sources: how an EAA the organization holds arrived.
const (
	HeldSourceQERDS      = "qerds"
	HeldSourceOpenID4VCI = "openid4vci"
	HeldSourceBootstrap  = "bootstrap"
)

// HeldAttestation is the org-scoped index over a credential the organization holds
// in its EUDI holder engine. The credential material itself lives in irmago (see
// .ai/features/attestations.md §6.5); this row points at it via CredentialRef and
// does not duplicate the claims.
type HeldAttestation struct {
	ID              uuid.UUID  `json:"id"`
	OrganizationID  uuid.UUID  `json:"organizationId"`
	CredentialRef   string     `json:"credentialRef"`
	VCT             string     `json:"vct"`
	Issuer          string     `json:"issuer"`
	Source          string     `json:"source"`
	SourceMessageID *uuid.UUID `json:"sourceMessageId,omitempty"`
	ReceivedAt      time.Time  `json:"receivedAt"`
	CreatedAt       time.Time  `json:"createdAt"`
}

// HeldInput records a newly received credential in the index.
type HeldInput struct {
	CredentialRef   string
	VCT             string
	Issuer          string
	Source          string
	SourceMessageID *uuid.UUID
}

const heldColumns = `id, organization_id, credential_ref, vct, issuer, source,
	source_message_id, received_at, created_at`

func scanHeld(row rowScanner) (HeldAttestation, error) {
	var h HeldAttestation
	if err := row.Scan(&h.ID, &h.OrganizationID, &h.CredentialRef, &h.VCT, &h.Issuer,
		&h.Source, &h.SourceMessageID, &h.ReceivedAt, &h.CreatedAt); err != nil {
		return HeldAttestation{}, err
	}
	return h, nil
}

// RecordHeld inserts a held-credential index row (used by the receive flows and the
// dev seed). The credential material itself is stored by the holder engine.
func (s *Store) RecordHeld(ctx context.Context, orgID uuid.UUID, in HeldInput) (HeldAttestation, error) {
	const query = `INSERT INTO held_attestations
		(organization_id, credential_ref, vct, issuer, source, source_message_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING ` + heldColumns
	h, err := scanHeld(s.db.QueryRow(ctx, query,
		orgID, in.CredentialRef, in.VCT, in.Issuer, in.Source, in.SourceMessageID))
	if err != nil {
		return HeldAttestation{}, fmt.Errorf("attestation: record held org %s: %w", orgID, err)
	}
	return h, nil
}

// HeldForMessage reports whether an active held credential already indexes the
// given source QERDS message. The receive flow uses it as an idempotency guard so
// a re-delivered credential offer is not redeemed and recorded twice.
func (s *Store) HeldForMessage(ctx context.Context, orgID, messageID uuid.UUID) (bool, error) {
	const query = `SELECT EXISTS (
		SELECT 1 FROM held_attestations
		WHERE organization_id = $1 AND source_message_id = $2 AND deleted_at IS NULL)`
	var exists bool
	if err := s.db.QueryRow(ctx, query, orgID, messageID).Scan(&exists); err != nil {
		return false, fmt.Errorf("attestation: held for message %s org %s: %w", messageID, orgID, err)
	}
	return exists, nil
}

// GetHeld returns a single active held credential by id. Returns ErrHeldNotFound
// when the row is absent or already soft-deleted.
func (s *Store) GetHeld(ctx context.Context, orgID, id uuid.UUID) (HeldAttestation, error) {
	const query = `SELECT ` + heldColumns + ` FROM held_attestations
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`
	h, err := scanHeld(s.db.QueryRow(ctx, query, id, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return HeldAttestation{}, ErrHeldNotFound
	}
	if err != nil {
		return HeldAttestation{}, fmt.Errorf("attestation: get held %s org %s: %w", id, orgID, err)
	}
	return h, nil
}

// ListHeld returns an organization's active (not soft-deleted) held credentials.
func (s *Store) ListHeld(ctx context.Context, orgID uuid.UUID) ([]HeldAttestation, error) {
	const query = `SELECT ` + heldColumns + ` FROM held_attestations
		WHERE organization_id = $1 AND deleted_at IS NULL
		ORDER BY received_at DESC`
	rows, err := s.db.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("attestation: list held org %s: %w", orgID, err)
	}
	defer rows.Close()

	held := []HeldAttestation{}
	for rows.Next() {
		h, err := scanHeld(rows)
		if err != nil {
			return nil, fmt.Errorf("attestation: list held scan: %w", err)
		}
		held = append(held, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("attestation: list held rows: %w", err)
	}
	return held, nil
}

// SoftDeleteHeld marks a held credential deleted (keeps the trail, Art 5(1)(m)) and
// audits it. Returns ErrHeldNotFound when the row is absent or already deleted.
func (s *Store) SoftDeleteHeld(ctx context.Context, orgID, id uuid.UUID) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE held_attestations SET deleted_at = now(), updated_at = now()
			WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
			RETURNING vct`
		var vct string
		err := q.QueryRow(ctx, update, id, orgID).Scan(&vct)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrHeldNotFound
		}
		if err != nil {
			return fmt.Errorf("attestation: soft-delete held %s org %s: %w", id, orgID, err)
		}
		return s.audit.Record(ctx, q, audit.AttestationHeldDeleted,
			audit.Target{Type: audit.TargetHeldAttestation, ID: id.String(), OrgID: &orgID},
			audit.Deleted(map[string]any{"vct": vct}))
	})
}
