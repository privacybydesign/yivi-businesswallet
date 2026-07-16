package organization

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

const (
	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"

	membershipDepartmentFK = "memberships_department_fkey"
)

type Store struct {
	db    database.DB
	audit audit.Recorder
}

func NewStore(db database.DB, recorder audit.Recorder) *Store {
	return &Store{db: db, audit: recorder}
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
	var org Organization
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO organizations (name, slug) VALUES ($1, $2) RETURNING id, name, slug`
		err := q.QueryRow(ctx, insert, name, slug).Scan(&org.ID, &org.Name, &org.Slug)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return ErrSlugTaken
		}
		if err != nil {
			return fmt.Errorf("organization: create %q: %w", slug, err)
		}
		return s.audit.Record(ctx, q, audit.OrganizationCreated,
			audit.Target{Type: audit.TargetOrganization, ID: org.ID.String(), OrgID: &org.ID},
			audit.Created(map[string]any{"name": name, "slug": slug}))
	})
	return org, err
}

// Delete removes an organization. All org-scoped data (memberships, invitations,
// departments, qerds messages/addresses, wallet instances/representations)
// cascades via FK ON DELETE CASCADE; audit events survive with a null org id.
func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const del = `DELETE FROM organizations WHERE id = $1 RETURNING name, slug`
		var name, slug string
		err := q.QueryRow(ctx, del, id).Scan(&name, &slug)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("organization: delete %s: %w", id, err)
		}
		// OrgID is left nil: the organization row is already deleted in this tx, so
		// the audit event cannot reference it (the FK would fail). The org id is
		// still recorded as the target id.
		return s.audit.Record(ctx, q, audit.OrganizationDeleted,
			audit.Target{Type: audit.TargetOrganization, ID: id.String()},
			audit.Deleted(map[string]any{"name": name, "slug": slug}))
	})
}

func (s *Store) Update(ctx context.Context, id uuid.UUID, name string) (Organization, error) {
	var org Organization
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `
			WITH old AS (SELECT name FROM organizations WHERE id = $1 FOR UPDATE)
			UPDATE organizations o SET name = $2, updated_at = now()
			FROM old WHERE o.id = $1
			RETURNING o.id, o.name, o.slug, old.name`
		var oldName string
		err := q.QueryRow(ctx, update, id, name).Scan(&org.ID, &org.Name, &org.Slug, &oldName)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("organization: update %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.OrganizationUpdated,
			audit.Target{Type: audit.TargetOrganization, ID: id.String(), OrgID: &id},
			audit.Updated(map[string]any{"name": oldName}, map[string]any{"name": name}))
	})
	return org, err
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
