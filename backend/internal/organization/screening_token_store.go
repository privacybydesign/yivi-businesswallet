package organization

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// EnsureVogToken mints or rotates the one live VOG submission token for a
// membership, mirroring EnsureReverifyToken: an admin's "request VOG", the
// scheduler's reminder mail and the member's own banner all call this, and
// each rotates the link so an older copy stops working.
func (s *Store) EnsureVogToken(ctx context.Context, orgID, userID uuid.UUID) (string, time.Time, error) {
	var raw string
	var expiresAt time.Time
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		var err error
		raw, expiresAt, err = ensureVogTokenTx(ctx, q, orgID, userID)
		return err
	})
	return raw, expiresAt, err
}

// ensureVogTokenTx shares the re-identification token's shape (32 random
// bytes, stored as a SHA-256 hash) - only the table differs.
func ensureVogTokenTx(ctx context.Context, q database.Querier, orgID, userID uuid.UUID) (string, time.Time, error) {
	rawToken, tokenHash, err := newReverifyToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := time.Now().Add(DefaultVogTokenTTL)
	const upsert = `
		INSERT INTO vog_submit_tokens (user_id, organization_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, organization_id) DO UPDATE SET
			token_hash = EXCLUDED.token_hash, expires_at = EXCLUDED.expires_at, created_at = now()`
	if _, err := q.Exec(ctx, upsert, userID, orgID, tokenHash[:], expiresAt); err != nil {
		return "", time.Time{}, fmt.Errorf("organization: ensure vog token user %s org %s: %w", userID, orgID, err)
	}
	return rawToken, expiresAt, nil
}

// VogTokenContext is what a VOG submission token resolves to: the membership
// it acts for, plus what the public page greets the member with.
type VogTokenContext struct {
	OrganizationID   uuid.UUID
	OrganizationName string
	OrganizationSlug string
	UserID           uuid.UUID
	Email            string
}

// VogTokenLookup resolves a raw VOG submission token to its membership, or
// ErrVogTokenNotFound for an unknown or expired token.
func (s *Store) VogTokenLookup(ctx context.Context, rawToken string) (VogTokenContext, error) {
	hash := hashReverifyToken(rawToken)
	const q = `
		SELECT t.organization_id, o.name, o.slug, t.user_id, u.email
		FROM vog_submit_tokens t
		JOIN organizations o ON o.id = t.organization_id
		JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = $1 AND t.expires_at > now()`
	var tc VogTokenContext
	err := s.db.QueryRow(ctx, q, hash[:]).Scan(
		&tc.OrganizationID, &tc.OrganizationName, &tc.OrganizationSlug, &tc.UserID, &tc.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return VogTokenContext{}, ErrVogTokenNotFound
	}
	if err != nil {
		return VogTokenContext{}, fmt.Errorf("organization: vog token lookup: %w", err)
	}
	return tc, nil
}
