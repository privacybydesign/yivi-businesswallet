package issuerstatus

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// indexAttempts bounds the retries for a random index that is already taken.
const indexAttempts = 8

type Store struct {
	db database.DB
}

func NewStore(db database.DB) *Store {
	return &Store{db: db}
}

func (s *Store) take(ctx context.Context, issuanceID string, expiresOn *time.Time) (int64, error) {
	// A cancelled, declined or expired offer gets no entry: its wallet cannot
	// redeem it anymore.
	const offer = `SELECT id FROM issued_attestations WHERE issuance_id = $1 AND status IN ('offered', 'claimed')`
	var issuedID uuid.UUID
	err := s.db.QueryRow(ctx, offer, issuanceID).Scan(&issuedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrUnknownOffer
	}
	if err != nil {
		return 0, fmt.Errorf("issuerstatus: find offer %s: %w", issuanceID, err)
	}
	const insert = `INSERT INTO issuer_status_entries (status_index, issued_attestation_id, expires_on)
		VALUES ($1, $2, $3)
		ON CONFLICT (status_index) DO NOTHING`
	limit := big.NewInt(listSize * listCount)
	for range indexAttempts {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return 0, err
		}
		idx := n.Int64()
		tag, err := s.db.Exec(ctx, insert, idx, issuedID, expiresOn)
		if err != nil {
			return 0, fmt.Errorf("issuerstatus: reserve entry for %s: %w", issuanceID, err)
		}
		if tag.RowsAffected() == 1 {
			return idx, nil
		}
	}
	return 0, ErrIndexExhausted
}

func (s *Store) taken(ctx context.Context, issuanceID string) (bool, error) {
	const query = `SELECT EXISTS (SELECT 1 FROM issuer_status_entries e
		JOIN issued_attestations a ON a.id = e.issued_attestation_id
		WHERE a.issuance_id = $1)`
	var ok bool
	if err := s.db.QueryRow(ctx, query, issuanceID).Scan(&ok); err != nil {
		return false, fmt.Errorf("issuerstatus: entry for %s: %w", issuanceID, err)
	}
	return ok, nil
}

// revoked returns the list-local indexes of the revoked credentials in list.
func (s *Store) revoked(ctx context.Context, list int64) ([]int64, error) {
	const query = `SELECT e.status_index - $1 FROM issuer_status_entries e
		JOIN issued_attestations a ON a.id = e.issued_attestation_id
		WHERE e.status_index >= $1 AND e.status_index < $2 AND a.status = 'revoked'`
	start := list * listSize
	rows, err := s.db.Query(ctx, query, start, start+listSize)
	if err != nil {
		return nil, fmt.Errorf("issuerstatus: status list %d: %w", list, err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, fmt.Errorf("issuerstatus: status list %d rows: %w", list, err)
	}
	return out, nil
}
