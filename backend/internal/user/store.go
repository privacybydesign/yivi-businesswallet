package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

type Store struct {
	db database.DB
}

func NewStore(db database.DB) *Store {
	return &Store{db: db}
}

func (s *Store) FindOrCreateByEmail(ctx context.Context, email string) (User, error) {
	const q = `
		INSERT INTO users (email)
		VALUES ($1)
		ON CONFLICT (email)
		DO UPDATE SET email = EXCLUDED.email
		RETURNING id, email`
	var u User
	if err := s.db.QueryRow(ctx, q, email).Scan(&u.ID, &u.Email); err != nil {
		return User{}, fmt.Errorf("user: find or create by email: %w", err)
	}
	return u, nil
}

func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (User, error) {
	const q = `SELECT id, email FROM users WHERE id = $1`
	var u User
	if err := s.db.QueryRow(ctx, q, id).Scan(&u.ID, &u.Email); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("user: get by id %s: %w", id, err)
	}
	return u, nil
}
