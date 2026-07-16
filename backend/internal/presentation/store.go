// Package presentation stores the mapping from our opaque, client-facing session
// id to the verifier-minted transaction_id. The transaction_id is an externally
// minted identifier that must never become the bearer the client presents to the
// unauthenticated claim endpoint; keeping it server-side and handing the client
// an independent random id decouples the two (see .ai/features/auth-openid4vp.md).
package presentation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// ErrNotFound covers unknown and expired presentation sessions alike: not
// distinguishing them avoids leaking whether a session ever existed, and both map
// to the same caller-facing outcome (pending / not-yet-finished).
var ErrNotFound = errors.New("presentation: unknown or expired session")

type Store struct {
	db  database.DB
	ttl time.Duration
}

func NewStore(db database.DB, ttl time.Duration) *Store {
	return &Store{db: db, ttl: ttl}
}

// Create records a started presentation and returns the opaque id the client
// polls / claims with. Only the id's hash is stored.
func (s *Store) Create(ctx context.Context, transactionID string) (string, error) {
	raw, hash, err := newID()
	if err != nil {
		return "", fmt.Errorf("presentation: mint id: %w", err)
	}

	const q = `
		INSERT INTO presentation_sessions (id_hash, transaction_id, expires_at)
		VALUES ($1, $2, $3)`
	expiresAt := time.Now().Add(s.ttl)
	if _, err := s.db.Exec(ctx, q, hash[:], transactionID, expiresAt); err != nil {
		return "", fmt.Errorf("presentation: create: %w", err)
	}
	return raw, nil
}

// TransactionID resolves the client-facing id to the verifier transaction id,
// returning ErrNotFound for an unknown or expired id.
func (s *Store) TransactionID(ctx context.Context, id string) (string, error) {
	hash := hashID(id)

	const q = `
		SELECT transaction_id
		FROM presentation_sessions
		WHERE id_hash = $1 AND expires_at > now()`
	var transactionID string
	if err := s.db.QueryRow(ctx, q, hash[:]).Scan(&transactionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("presentation: lookup: %w", err)
	}
	return transactionID, nil
}

// DeleteExpired prunes expired rows in batch (mirrors session.Store).
func (s *Store) DeleteExpired(ctx context.Context) (int64, error) {
	const q = `DELETE FROM presentation_sessions WHERE expires_at < now()`
	tag, err := s.db.Exec(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("presentation: prune expired: %w", err)
	}
	return tag.RowsAffected(), nil
}
