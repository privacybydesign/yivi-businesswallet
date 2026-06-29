package organization

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
)

func (s *Store) AddMembership(ctx context.Context, orgID, userID uuid.UUID, role string, jobTitle *string, departmentID *uuid.UUID) (Member, error) {
	const q = `INSERT INTO memberships (organization_id, user_id, role, job_title, department_id) VALUES ($1, $2, $3, $4, $5)`
	_, err := s.db.Exec(ctx, q, orgID, userID, role, jobTitle, departmentID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == uniqueViolation:
			return Member{}, ErrAlreadyMember
		case pgErr.Code == foreignKeyViolation && pgErr.ConstraintName == membershipDepartmentFK:
			return Member{}, ErrDepartmentNotFound
		}
	}
	if err != nil {
		return Member{}, fmt.Errorf("organization: add membership user %s org %s: %w", userID, orgID, err)
	}
	return s.getMember(ctx, orgID, userID)
}

func (s *Store) GetMembership(ctx context.Context, userID, orgID uuid.UUID) (Membership, error) {
	const q = `SELECT user_id, organization_id, role, job_title, department_id FROM memberships WHERE user_id = $1 AND organization_id = $2`
	var m Membership
	err := s.db.QueryRow(ctx, q, userID, orgID).Scan(&m.UserID, &m.OrganizationID, &m.Role, &m.JobTitle, &m.DepartmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrNotMember
	}
	if err != nil {
		return Membership{}, fmt.Errorf("organization: get membership user %s org %s: %w", userID, orgID, err)
	}
	return m, nil
}

func (s *Store) ListMembers(ctx context.Context, orgID uuid.UUID) ([]Member, error) {
	const q = `
		SELECT u.id, u.email, u.preferred_name, u.given_names, u.name_prefix, u.last_name,
		       m.role, m.job_title, m.department_id, d.name
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		LEFT JOIN departments d ON d.id = m.department_id
		WHERE m.organization_id = $1
		ORDER BY u.last_name, u.given_names`
	rows, err := s.db.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("organization: list members org %s: %w", orgID, err)
	}
	defer rows.Close()

	members := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Email, &m.PreferredName, &m.GivenNames, &m.NamePrefix, &m.LastName,
			&m.Role, &m.JobTitle, &m.DepartmentID, &m.DepartmentName); err != nil {
			return nil, fmt.Errorf("organization: list members scan: %w", err)
		}
		members = append(members, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization: list members rows: %w", err)
	}

	return members, nil
}

func (s *Store) getMember(ctx context.Context, orgID, userID uuid.UUID) (Member, error) {
	const q = `
		SELECT u.id, u.email, u.preferred_name, u.given_names, u.name_prefix, u.last_name,
		       m.role, m.job_title, m.department_id, d.name
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		LEFT JOIN departments d ON d.id = m.department_id
		WHERE m.organization_id = $1 AND m.user_id = $2`
	var m Member
	err := s.db.QueryRow(ctx, q, orgID, userID).Scan(&m.UserID, &m.Email, &m.PreferredName, &m.GivenNames, &m.NamePrefix, &m.LastName,
		&m.Role, &m.JobTitle, &m.DepartmentID, &m.DepartmentName)
	if errors.Is(err, pgx.ErrNoRows) {
		return Member{}, ErrNotMember
	}
	if err != nil {
		return Member{}, fmt.Errorf("organization: get member user %s org %s: %w", userID, orgID, err)
	}
	return m, nil
}

func (s *Store) UpdateMembership(ctx context.Context, orgID, userID uuid.UUID, role *string, jobTitle *string, departmentID *uuid.UUID) (Member, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Member{}, fmt.Errorf("organization: begin update membership user %s org %s: %w", userID, orgID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current string
	var oldJobTitle *string
	var oldDeptID *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT role, job_title, department_id FROM memberships WHERE organization_id = $1 AND user_id = $2`, orgID, userID).
		Scan(&current, &oldJobTitle, &oldDeptID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Member{}, ErrNotMember
	}
	if err != nil {
		return Member{}, fmt.Errorf("organization: read membership user %s org %s: %w", userID, orgID, err)
	}

	if role != nil && *role == RoleMember && current == RoleAdmin {
		admins, err := lockAndCountAdmins(ctx, tx, orgID)
		if err != nil {
			return Member{}, err
		}
		if admins <= 1 {
			return Member{}, ErrLastAdmin
		}
	}

	const q = `UPDATE memberships SET role = COALESCE($3, role), job_title = $4, department_id = $5 WHERE organization_id = $1 AND user_id = $2`
	tag, err := tx.Exec(ctx, q, orgID, userID, role, jobTitle, departmentID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation && pgErr.ConstraintName == membershipDepartmentFK {
		return Member{}, ErrDepartmentNotFound
	}
	if err != nil {
		return Member{}, fmt.Errorf("organization: update membership user %s org %s: %w", userID, orgID, err)
	}
	if tag.RowsAffected() == 0 {
		return Member{}, ErrNotMember
	}

	newRole := current
	if role != nil {
		newRole = *role
	}
	if err := s.audit.Record(ctx, tx, audit.MembershipRoleChanged,
		audit.Target{Type: audit.TargetMembership, ID: userID.String(), OrgID: &orgID},
		audit.Updated(
			map[string]any{"role": current, "jobTitle": oldJobTitle, "departmentId": oldDeptID},
			map[string]any{"role": newRole, "jobTitle": jobTitle, "departmentId": departmentID})); err != nil {
		return Member{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Member{}, fmt.Errorf("organization: commit update membership user %s org %s: %w", userID, orgID, err)
	}
	return s.getMember(ctx, orgID, userID)
}

// ORDER BY user_id is load-bearing: it makes concurrent demotions take the row
// locks in the same order, so they serialize instead of deadlocking.
func lockAndCountAdmins(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) (int, error) {
	rows, err := tx.Query(ctx, `SELECT user_id FROM memberships WHERE organization_id = $1 AND role = $2 ORDER BY user_id FOR UPDATE`, orgID, RoleAdmin)
	if err != nil {
		return 0, fmt.Errorf("organization: lock admins org %s: %w", orgID, err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("organization: count admins org %s: %w", orgID, err)
	}
	return count, nil
}
