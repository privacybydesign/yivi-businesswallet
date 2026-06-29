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

func (s *Store) CreateDepartment(ctx context.Context, orgID uuid.UUID, name string) (Department, error) {
	var d Department
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO departments (organization_id, name) VALUES ($1, $2) RETURNING id, organization_id, name`
		err := q.QueryRow(ctx, insert, orgID, name).Scan(&d.ID, &d.OrganizationID, &d.Name)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return ErrDepartmentNameTaken
		}
		if err != nil {
			return fmt.Errorf("organization: create department org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.DepartmentCreated,
			audit.Target{Type: audit.TargetDepartment, ID: d.ID.String(), OrgID: &orgID},
			audit.Created(map[string]any{"name": name}))
	})
	return d, err
}

func (s *Store) ListDepartments(ctx context.Context, orgID uuid.UUID) ([]Department, error) {
	const q = `SELECT id, organization_id, name FROM departments WHERE organization_id = $1 ORDER BY name`
	rows, err := s.db.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("organization: list departments org %s: %w", orgID, err)
	}
	defer rows.Close()

	departments := []Department{}
	for rows.Next() {
		var d Department
		if err := rows.Scan(&d.ID, &d.OrganizationID, &d.Name); err != nil {
			return nil, fmt.Errorf("organization: list departments scan: %w", err)
		}
		departments = append(departments, d)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization: list departments rows: %w", err)
	}

	return departments, nil
}

func (s *Store) UpdateDepartment(ctx context.Context, orgID, deptID uuid.UUID, name string) (Department, error) {
	var d Department
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `
			WITH old AS (SELECT name FROM departments WHERE id = $1 AND organization_id = $2 FOR UPDATE)
			UPDATE departments d SET name = $3, updated_at = now()
			FROM old WHERE d.id = $1 AND d.organization_id = $2
			RETURNING d.id, d.organization_id, d.name, old.name`
		var oldName string
		err := q.QueryRow(ctx, update, deptID, orgID, name).Scan(&d.ID, &d.OrganizationID, &d.Name, &oldName)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return ErrDepartmentNameTaken
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDepartmentNotFound
		}
		if err != nil {
			return fmt.Errorf("organization: update department %s org %s: %w", deptID, orgID, err)
		}
		return s.audit.Record(ctx, q, audit.DepartmentUpdated,
			audit.Target{Type: audit.TargetDepartment, ID: deptID.String(), OrgID: &orgID},
			audit.Updated(map[string]any{"name": oldName}, map[string]any{"name": name}))
	})
	return d, err
}

func (s *Store) DeleteDepartment(ctx context.Context, orgID, deptID uuid.UUID) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const del = `DELETE FROM departments WHERE id = $1 AND organization_id = $2 RETURNING name`
		var name string
		err := q.QueryRow(ctx, del, deptID, orgID).Scan(&name)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
			return ErrDepartmentInUse
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDepartmentNotFound
		}
		if err != nil {
			return fmt.Errorf("organization: delete department %s org %s: %w", deptID, orgID, err)
		}
		return s.audit.Record(ctx, q, audit.DepartmentDeleted,
			audit.Target{Type: audit.TargetDepartment, ID: deptID.String(), OrgID: &orgID},
			audit.Deleted(map[string]any{"name": name}))
	})
}
