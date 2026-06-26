package session

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// ErrInvalidSession covers unknown, forged, and expired sessions alike: not
// distinguishing them avoids leaking whether a session ever existed.
var ErrInvalidSession = errors.New("session: invalid or expired")

type Store struct {
	db  database.DB
	ttl time.Duration
}

func NewStore(db database.DB, ttl time.Duration) *Store {
	return &Store{db: db, ttl: ttl}
}

func (s *Store) Mint(ctx context.Context, userID uuid.UUID, idempotencyKey [sha256.Size]byte) (string, error) {
	raw, hash, err := newToken()
	if err != nil {
		return "", err
	}

	const q = `
		INSERT INTO sessions (token_hash, user_id, expires_at, idempotency_key)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (idempotency_key)
		DO UPDATE SET token_hash = EXCLUDED.token_hash,
		              expires_at = EXCLUDED.expires_at`
	expiresAt := time.Now().Add(s.ttl)
	if _, err := s.db.Exec(ctx, q, hash[:], userID, expiresAt, idempotencyKey[:]); err != nil {
		return "", fmt.Errorf("session: mint: %w", err)
	}
	return raw, nil
}

// Lookup never deletes expired rows on a miss: it runs on every authed request,
// so it stays read-only. DeleteExpired prunes them in batch instead.
func (s *Store) Lookup(ctx context.Context, rawToken string) (user.User, error) {
	hash := hashToken(rawToken)

	const q = `
		SELECT u.id, u.email
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`
	var u user.User
	if err := s.db.QueryRow(ctx, q, hash[:]).Scan(&u.ID, &u.Email); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return user.User{}, ErrInvalidSession
		}
		return user.User{}, fmt.Errorf("session: lookup: %w", err)
	}
	return u, nil
}

func (s *Store) Delete(ctx context.Context, rawToken string) error {
	hash := hashToken(rawToken)

	const q = `DELETE FROM sessions WHERE token_hash = $1`
	if _, err := s.db.Exec(ctx, q, hash[:]); err != nil {
		return fmt.Errorf("session: delete: %w", err)
	}
	return nil
}

func (s *Store) DeleteExpired(ctx context.Context) (int64, error) {
	const q = `DELETE FROM sessions WHERE expires_at < now()`
	tag, err := s.db.Exec(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("session: prune expired: %w", err)
	}
	return tag.RowsAffected(), nil
}
