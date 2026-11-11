package walletunit

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

const (
	uniqueViolation = "23505"
	// indexAttempts bounds the retries for a random index that is already taken;
	// it only runs out when the lists are close to full.
	indexAttempts = 8
)

// Store persists wallet units, the attestations issued to them and the status
// their list entries derive from.
type Store struct {
	db    database.DB
	audit audit.Recorder
}

func NewStore(db database.DB, recorder audit.Recorder) *Store {
	return &Store{db: db, audit: recorder}
}

const unitColumns = `id, status, revoked_reason, revoked_at, created_at`

func scanUnit(row pgx.Row) (Unit, error) {
	var u Unit
	err := row.Scan(&u.ID, &u.Status, &u.RevokedReason, &u.RevokedAt, &u.CreatedAt)
	return u, err
}

// activeUnitForIssue returns the org's active unit, creating the first one when
// the org never had any. An org whose unit was revoked gets ErrUnitRevoked until an
// admin activates a new one.
func (s *Store) activeUnitForIssue(ctx context.Context, orgID uuid.UUID) (Unit, error) {
	const insertFirst = `INSERT INTO wallet_units (organization_id)
		SELECT $1 WHERE NOT EXISTS (SELECT 1 FROM wallet_units WHERE organization_id = $1)
		ON CONFLICT DO NOTHING`
	if _, err := s.db.Exec(ctx, insertFirst, orgID); err != nil {
		return Unit{}, fmt.Errorf("walletunit: create first unit org %s: %w", orgID, err)
	}
	const active = `SELECT ` + unitColumns + ` FROM wallet_units WHERE organization_id = $1 AND status = 'active'`
	u, err := scanUnit(s.db.QueryRow(ctx, active, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Unit{}, ErrUnitRevoked
	}
	if err != nil {
		return Unit{}, fmt.Errorf("walletunit: active unit org %s: %w", orgID, err)
	}
	return u, nil
}

// recordAttestation stores an issued attestation under a free random status index
// and returns that index.
func (s *Store) recordAttestation(ctx context.Context, unitID uuid.UUID, kind string, expiresAt, statusExpiresAt time.Time) (int64, error) {
	const insert = `INSERT INTO wallet_unit_attestations (wallet_unit_id, kind, status_index, expires_at, status_expires_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (status_index) DO NOTHING`
	limit := big.NewInt(statusListSize * statusListCount)
	for range indexAttempts {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return 0, err
		}
		idx := n.Int64()
		tag, err := s.db.Exec(ctx, insert, unitID, kind, idx, expiresAt, statusExpiresAt)
		if err != nil {
			return 0, fmt.Errorf("walletunit: record %s unit %s: %w", kind, unitID, err)
		}
		if tag.RowsAffected() == 1 {
			return idx, nil
		}
	}
	return 0, ErrIndexExhausted
}

// Summary returns the org's latest wallet unit with its issuance count, or
// ErrNoActiveUnit when the org never had one.
func (s *Store) Summary(ctx context.Context, orgID uuid.UUID) (Summary, error) {
	const query = `SELECT u.id, u.status, u.revoked_reason, u.revoked_at, u.created_at,
			(SELECT count(*) FROM wallet_unit_attestations a WHERE a.wallet_unit_id = u.id),
			(SELECT max(a.issued_at) FROM wallet_unit_attestations a WHERE a.wallet_unit_id = u.id)
		FROM wallet_units u WHERE u.organization_id = $1
		ORDER BY (u.status = 'active') DESC, u.created_at DESC LIMIT 1`
	var out Summary
	u := &out.Unit
	err := s.db.QueryRow(ctx, query, orgID).Scan(&u.ID, &u.Status, &u.RevokedReason, &u.RevokedAt, &u.CreatedAt,
		&out.IssuedCount, &out.LastIssuedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Summary{}, ErrNoActiveUnit
	}
	if err != nil {
		return Summary{}, fmt.Errorf("walletunit: summary org %s: %w", orgID, err)
	}
	return out, nil
}

// Revoke revokes the org's active unit and audits, in one tx. Every entry of
// every attestation issued to it then reads invalid.
func (s *Store) Revoke(ctx context.Context, orgID uuid.UUID, reason string) (Unit, error) {
	if !validReason(reason) {
		return Unit{}, ErrInvalidReason
	}
	var out Unit
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE wallet_units SET status = 'revoked', revoked_reason = $2, revoked_at = now()
			WHERE organization_id = $1 AND status = 'active'
			RETURNING ` + unitColumns
		var err error
		out, err = scanUnit(q.QueryRow(ctx, update, orgID, reason))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoActiveUnit
		}
		if err != nil {
			return fmt.Errorf("walletunit: revoke org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.WalletUnitRevoked,
			audit.Target{Type: audit.TargetWalletUnit, ID: out.ID.String(), OrgID: &orgID},
			audit.Updated(map[string]any{"status": UnitActive}, map[string]any{"status": out.Status, "reason": reason}))
	})
	return out, err
}

// Activate creates a new active unit for an org whose unit was revoked, and
// audits, in one tx.
func (s *Store) Activate(ctx context.Context, orgID uuid.UUID) (Unit, error) {
	var out Unit
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO wallet_units (organization_id) VALUES ($1) RETURNING ` + unitColumns
		var err error
		out, err = scanUnit(q.QueryRow(ctx, insert, orgID))
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return ErrUnitActive
		}
		if err != nil {
			return fmt.Errorf("walletunit: activate org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.WalletUnitActivated,
			audit.Target{Type: audit.TargetWalletUnit, ID: out.ID.String(), OrgID: &orgID},
			audit.Created(map[string]any{"status": out.Status}))
	})
	return out, err
}

// listStatuses returns the non-valid entries of status list `list`, keyed by
// their index within it. An entry is invalid when its unit is revoked or its org
// is revoked or deleted, and suspended while its org is suspended.
func (s *Store) listStatuses(ctx context.Context, list int64) (map[int64]byte, error) {
	const query = `SELECT a.status_index - $1,
			CASE WHEN u.status = 'revoked' OR o.id IS NULL OR o.status = 'revoked' THEN $3::smallint
			     ELSE $4::smallint END
		FROM wallet_unit_attestations a
		JOIN wallet_units u ON u.id = a.wallet_unit_id
		LEFT JOIN organizations o ON o.id = u.organization_id
		WHERE a.status_index >= $1 AND a.status_index < $2
		  AND (u.status = 'revoked' OR o.id IS NULL OR o.status <> 'active')`
	start := list * statusListSize
	rows, err := s.db.Query(ctx, query, start, start+statusListSize, int16(statusInvalid), int16(statusSuspended))
	if err != nil {
		return nil, fmt.Errorf("walletunit: status list %d: %w", list, err)
	}
	defer rows.Close()
	out := map[int64]byte{}
	for rows.Next() {
		var idx int64
		var status int16
		if err := rows.Scan(&idx, &status); err != nil {
			return nil, fmt.Errorf("walletunit: status list %d scan: %w", list, err)
		}
		out[idx] = byte(status)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("walletunit: status list %d rows: %w", list, err)
	}
	return out, nil
}
