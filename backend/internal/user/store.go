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

func (s *Store) FindByEmail(ctx context.Context, email string) (User, error) {
	const q = `SELECT id, email, preferred_name, given_names, name_prefix, last_name FROM users WHERE email = $1`
	var u User
	if err := s.db.QueryRow(ctx, q, email).Scan(&u.ID, &u.Email, &u.PreferredName, &u.GivenNames, &u.NamePrefix, &u.LastName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("user: find by email: %w", err)
	}
	return u, nil
}

func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (User, error) {
	const q = `SELECT id, email, preferred_name, given_names, name_prefix, last_name FROM users WHERE id = $1`
	var u User
	if err := s.db.QueryRow(ctx, q, id).Scan(&u.ID, &u.Email, &u.PreferredName, &u.GivenNames, &u.NamePrefix, &u.LastName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("user: get by id %s: %w", id, err)
	}
	return u, nil
}
