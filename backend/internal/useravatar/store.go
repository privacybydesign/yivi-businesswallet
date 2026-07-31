package useravatar

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// Store persists one avatar per user.
type Store struct {
	db database.DB
}

func NewStore(db database.DB) *Store {
	return &Store{db: db}
}

// Get returns a user's own avatar, or ErrNoAvatar when they have none.
func (s *Store) Get(ctx context.Context, userID uuid.UUID) (Avatar, error) {
	const q = `SELECT bytes, content_type, updated_at FROM user_avatars WHERE user_id = $1`
	var a Avatar
	err := s.db.QueryRow(ctx, q, userID).Scan(&a.Bytes, &a.ContentType, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Avatar{}, ErrNoAvatar
	}
	if err != nil {
		return Avatar{}, fmt.Errorf("useravatar: get user %s: %w", userID, err)
	}
	return a, nil
}

// GetForOrgMember returns a user's avatar only when that user is a member of
// orgID, so an administrator of one organisation cannot read the photo of
// someone who never joined it. A non-member is indistinguishable from a member
// without an avatar: both are ErrNoAvatar.
func (s *Store) GetForOrgMember(ctx context.Context, userID, orgID uuid.UUID) (Avatar, error) {
	const q = `SELECT a.bytes, a.content_type, a.updated_at
		FROM user_avatars a
		JOIN memberships m ON m.user_id = a.user_id
		WHERE a.user_id = $1 AND m.organization_id = $2`
	var a Avatar
	err := s.db.QueryRow(ctx, q, userID, orgID).Scan(&a.Bytes, &a.ContentType, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Avatar{}, ErrNoAvatar
	}
	if err != nil {
		return Avatar{}, fmt.Errorf("useravatar: get member %s org %s: %w", userID, orgID, err)
	}
	return a, nil
}

// Save stores or replaces a user's avatar and returns the new updated_at, which
// versions the URL the image is served from.
func (s *Store) Save(ctx context.Context, userID uuid.UUID, a Avatar) (time.Time, error) {
	const q = `INSERT INTO user_avatars (user_id, bytes, content_type)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET
			bytes = EXCLUDED.bytes,
			content_type = EXCLUDED.content_type,
			updated_at = now()
		RETURNING updated_at`
	var updatedAt time.Time
	if err := s.db.QueryRow(ctx, q, userID, a.Bytes, a.ContentType).Scan(&updatedAt); err != nil {
		return time.Time{}, fmt.Errorf("useravatar: save user %s: %w", userID, err)
	}
	return updatedAt, nil
}

// Delete removes a user's avatar. Removing an avatar that is not there is not an
// error — the caller asked for "no avatar" and that is the result.
func (s *Store) Delete(ctx context.Context, userID uuid.UUID) error {
	const q = `DELETE FROM user_avatars WHERE user_id = $1`
	if _, err := s.db.Exec(ctx, q, userID); err != nil {
		return fmt.Errorf("useravatar: delete user %s: %w", userID, err)
	}
	return nil
}
