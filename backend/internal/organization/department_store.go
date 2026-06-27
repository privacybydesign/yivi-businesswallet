package organization

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *Store) CreateDepartment(ctx context.Context, orgID uuid.UUID, name string) (Department, error) {
	const q = `INSERT INTO departments (organization_id, name) VALUES ($1, $2) RETURNING id, organization_id, name`
	var d Department
	err := s.db.QueryRow(ctx, q, orgID, name).Scan(&d.ID, &d.OrganizationID, &d.Name)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return Department{}, ErrDepartmentNameTaken
	}
	if err != nil {
		return Department{}, fmt.Errorf("organization: create department org %s: %w", orgID, err)
	}
	return d, nil
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
	const q = `UPDATE departments SET name = $3, updated_at = now() WHERE id = $1 AND organization_id = $2 RETURNING id, organization_id, name`
	var d Department
	err := s.db.QueryRow(ctx, q, deptID, orgID, name).Scan(&d.ID, &d.OrganizationID, &d.Name)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return Department{}, ErrDepartmentNameTaken
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Department{}, ErrDepartmentNotFound
	}
	if err != nil {
		return Department{}, fmt.Errorf("organization: update department %s org %s: %w", deptID, orgID, err)
	}
	return d, nil
}

func (s *Store) DeleteDepartment(ctx context.Context, orgID, deptID uuid.UUID) error {
	const q = `DELETE FROM departments WHERE id = $1 AND organization_id = $2`
	tag, err := s.db.Exec(ctx, q, deptID, orgID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
		return ErrDepartmentInUse
	}
	if err != nil {
		return fmt.Errorf("organization: delete department %s org %s: %w", deptID, orgID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDepartmentNotFound
	}
	return nil
}
