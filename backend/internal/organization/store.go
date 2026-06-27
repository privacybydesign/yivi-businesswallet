package organization

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

const (
	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
)

type Store struct {
	db database.DB
}

func NewStore(db database.DB) *Store {
	return &Store{db: db}
}

func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (Organization, error) {
	var org Organization
	err := s.db.QueryRow(ctx, "SELECT id, name, slug FROM organizations WHERE id = $1", id).
		Scan(&org.ID, &org.Name, &org.Slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, fmt.Errorf("organization: get by id %s: %w", id, err)
	}
	return org, nil
}

func (s *Store) GetBySlug(ctx context.Context, slug string) (Organization, error) {
	var org Organization
	err := s.db.QueryRow(ctx, "SELECT id, name, slug FROM organizations WHERE slug = $1", slug).
		Scan(&org.ID, &org.Name, &org.Slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, fmt.Errorf("organization: get by slug %q: %w", slug, err)
	}
	return org, nil
}

func (s *Store) Create(ctx context.Context, name, slug string) (Organization, error) {
	const q = `INSERT INTO organizations (name, slug) VALUES ($1, $2) RETURNING id, name, slug`
	var org Organization
	err := s.db.QueryRow(ctx, q, name, slug).Scan(&org.ID, &org.Name, &org.Slug)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return Organization{}, ErrSlugTaken
	}
	if err != nil {
		return Organization{}, fmt.Errorf("organization: create %q: %w", slug, err)
	}
	return org, nil
}

func (s *Store) List(ctx context.Context) ([]Organization, error) {
	rows, err := s.db.Query(ctx, "SELECT id, name, slug FROM organizations ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("organization: list query: %w", err)
	}
	defer rows.Close()

	orgs := []Organization{}
	for rows.Next() {
		var org Organization
		if err := rows.Scan(&org.ID, &org.Name, &org.Slug); err != nil {
			return nil, fmt.Errorf("organization: list scan: %w", err)
		}
		orgs = append(orgs, org)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization: list rows: %w", err)
	}

	return orgs, nil
}

func (s *Store) ListForUser(ctx context.Context, userID uuid.UUID) ([]Organization, error) {
	const q = `
		SELECT o.id, o.name, o.slug
		FROM organizations o
		JOIN memberships m ON m.organization_id = o.id
		WHERE m.user_id = $1
		ORDER BY o.name`
	rows, err := s.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("organization: list for user %s: %w", userID, err)
	}
	defer rows.Close()

	orgs := []Organization{}
	for rows.Next() {
		var org Organization
		if err := rows.Scan(&org.ID, &org.Name, &org.Slug); err != nil {
			return nil, fmt.Errorf("organization: list for user scan: %w", err)
		}
		orgs = append(orgs, org)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization: list for user rows: %w", err)
	}

	return orgs, nil
}
