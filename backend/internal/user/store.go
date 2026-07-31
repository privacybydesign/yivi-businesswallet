package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

const uniqueViolation = "23505"

type Store struct {
	db database.DB
}

func NewStore(db database.DB) *Store {
	return &Store{db: db}
}

// selectColumns is the projection behind User. The avatar bytes are deliberately
// left out — only whether one is stored and when it changed, so a lookup on every
// authenticated request never carries an image.
const selectColumns = `id, email, preferred_name, given_names, last_name,
	avatar_bytes IS NOT NULL, avatar_updated_at`

func (s *Store) FindByEmail(ctx context.Context, email Email) (User, error) {
	const q = `SELECT ` + selectColumns + ` FROM users WHERE email = $1`
	var u User
	if err := s.db.QueryRow(ctx, q, email).Scan(&u.ID, &u.Email, &u.PreferredName, &u.GivenNames, &u.LastName,
		&u.HasAvatar, &u.AvatarUpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("user: find by email: %w", err)
	}
	return u, nil
}

func (s *Store) Create(ctx context.Context, u User) (User, error) {
	const q = `
		INSERT INTO users (email, preferred_name, given_names, last_name)
		VALUES ($1, $2, $3, $4)
		RETURNING ` + selectColumns
	var created User
	err := s.db.QueryRow(ctx, q, u.Email, u.PreferredName, u.GivenNames, u.LastName).
		Scan(&created.ID, &created.Email, &created.PreferredName, &created.GivenNames, &created.LastName,
			&created.HasAvatar, &created.AvatarUpdatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return User{}, ErrEmailTaken
	}
	if err != nil {
		return User{}, fmt.Errorf("user: create %q: %w", u.Email, err)
	}
	return created, nil
}

func (s *Store) UpdateName(ctx context.Context, id uuid.UUID, givenNames, lastName string) error {
	const q = `UPDATE users SET given_names = $2, last_name = $3, updated_at = now() WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, id, givenNames, lastName)
	if err != nil {
		return fmt.Errorf("user: update name %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (User, error) {
	const q = `SELECT ` + selectColumns + ` FROM users WHERE id = $1`
	var u User
	if err := s.db.QueryRow(ctx, q, id).Scan(&u.ID, &u.Email, &u.PreferredName, &u.GivenNames, &u.LastName,
		&u.HasAvatar, &u.AvatarUpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("user: get by id %s: %w", id, err)
	}
	return u, nil
}

// GetAvatar returns a user's stored avatar, or ErrNoAvatar when none is set.
func (s *Store) GetAvatar(ctx context.Context, id uuid.UUID) (Avatar, error) {
	const q = `SELECT avatar_bytes, avatar_content_type FROM users WHERE id = $1`
	var a Avatar
	if err := s.db.QueryRow(ctx, q, id).Scan(&a.Bytes, &a.ContentType); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Avatar{}, ErrNotFound
		}
		return Avatar{}, fmt.Errorf("user: get avatar %s: %w", id, err)
	}
	if len(a.Bytes) == 0 {
		return Avatar{}, ErrNoAvatar
	}
	return a, nil
}

// SetAvatar stores a normalised avatar, replacing any previous one, and returns
// the refreshed user so the caller can build the new versioned URL.
func (s *Store) SetAvatar(ctx context.Context, id uuid.UUID, a Avatar) (User, error) {
	const q = `UPDATE users
		SET avatar_bytes = $2, avatar_content_type = $3, avatar_updated_at = now(), updated_at = now()
		WHERE id = $1
		RETURNING ` + selectColumns
	return s.scanUpdatedUser(ctx, q, id, a.Bytes, a.ContentType)
}

// ClearAvatar removes a user's avatar, leaving the initials fallback in its place.
// Clearing an already-empty avatar is not an error — the end state is the same.
func (s *Store) ClearAvatar(ctx context.Context, id uuid.UUID) (User, error) {
	const q = `UPDATE users
		SET avatar_bytes = NULL, avatar_content_type = '', avatar_updated_at = NULL, updated_at = now()
		WHERE id = $1
		RETURNING ` + selectColumns
	return s.scanUpdatedUser(ctx, q, id)
}

func (s *Store) scanUpdatedUser(ctx context.Context, q string, id uuid.UUID, rest ...any) (User, error) {
	args := append([]any{id}, rest...)
	var u User
	if err := s.db.QueryRow(ctx, q, args...).Scan(&u.ID, &u.Email, &u.PreferredName, &u.GivenNames, &u.LastName,
		&u.HasAvatar, &u.AvatarUpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("user: update avatar %s: %w", id, err)
	}
	return u, nil
}
