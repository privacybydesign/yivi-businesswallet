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

func (s *Store) FindByEmail(ctx context.Context, email Email) (User, error) {
	const q = `SELECT id, email, preferred_name, given_names, last_name FROM users WHERE email = $1`
	var u User
	if err := s.db.QueryRow(ctx, q, email).Scan(&u.ID, &u.Email, &u.PreferredName, &u.GivenNames, &u.LastName); err != nil {
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
		RETURNING id, email, preferred_name, given_names, last_name`
	var created User
	err := s.db.QueryRow(ctx, q, u.Email, u.PreferredName, u.GivenNames, u.LastName).
		Scan(&created.ID, &created.Email, &created.PreferredName, &created.GivenNames, &created.LastName)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return User{}, ErrEmailTaken
	}
	if err != nil {
		return User{}, fmt.Errorf("user: create %q: %w", u.Email, err)
	}
	return created, nil
}

func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (User, error) {
	const q = `SELECT id, email, preferred_name, given_names, last_name FROM users WHERE id = $1`
	var u User
	if err := s.db.QueryRow(ctx, q, id).Scan(&u.ID, &u.Email, &u.PreferredName, &u.GivenNames, &u.LastName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("user: get by id %s: %w", id, err)
	}
	return u, nil
}
