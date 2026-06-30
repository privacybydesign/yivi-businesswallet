package organization

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	return s.GetMember(ctx, orgID, userID)
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
		SELECT u.id, u.email, u.preferred_name, u.given_names, u.last_name,
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
		if err := rows.Scan(&m.UserID, &m.Email, &m.PreferredName, &m.GivenNames, &m.LastName,
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

const defaultMemberSort = "name"

var memberSortColumns = map[string][]string{
	"name":       {"last_name", "given_names"},
	"email":      {"email"},
	"role":       {"role"},
	"department": {"department_name"},
	"status":     {"status"},
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// The per-branch status guard ($2 empty, or matching that branch) lets a status
// filter prune the excluded source instead of scanning and discarding it.
var memberEntriesCTE = fmt.Sprintf(`
WITH entries AS (
	SELECT '%[1]s'::text AS status, u.id AS user_id, NULL::uuid AS invitation_id,
	       u.email, u.preferred_name, u.given_names, u.last_name,
	       m.role, m.job_title, m.department_id, d.name AS department_name,
	       NULL::timestamptz AS expires_at, NULL::uuid AS invited_by
	FROM memberships m
	JOIN users u ON u.id = m.user_id
	LEFT JOIN departments d ON d.id = m.department_id
	WHERE m.organization_id = $1 AND ($2 = '' OR $2 = '%[1]s')
	UNION ALL
	SELECT '%[2]s'::text AS status, NULL::uuid AS user_id, i.id AS invitation_id,
	       i.email, NULL::text AS preferred_name, i.invited_given_names, i.invited_last_name,
	       i.role, i.job_title, i.department_id, d.name AS department_name,
	       i.expires_at, i.invited_by
	FROM invitations i
	LEFT JOIN departments d ON d.id = i.department_id
	WHERE i.organization_id = $1 AND ($2 = '' OR $2 = '%[2]s')
)`, StatusActive, StatusInvited)

const memberSearchWhere = `
WHERE $3::text IS NULL OR (
	   email ILIKE $3 ESCAPE '\'
	OR given_names ILIKE $3 ESCAPE '\'
	OR last_name ILIKE $3 ESCAPE '\'
	OR coalesce(job_title, '') ILIKE $3 ESCAPE '\'
	OR coalesce(department_name, '') ILIKE $3 ESCAPE '\'
)`

func memberOrderBy(sort string, desc bool) string {
	cols, ok := memberSortColumns[sort]
	if !ok {
		cols = memberSortColumns[defaultMemberSort]
	}
	dir := "ASC"
	if desc {
		dir = "DESC"
	}
	parts := make([]string, 0, len(cols)+2)
	for _, c := range cols {
		parts = append(parts, c+" "+dir+" NULLS LAST")
	}
	parts = append(parts, "email ASC", "status ASC")
	return strings.Join(parts, ", ")
}

func (s *Store) ListMemberEntries(ctx context.Context, orgID uuid.UUID, p MemberListParams) ([]MemberEntry, int, error) {
	var pattern *string
	if p.Search != "" {
		like := "%" + likeEscaper.Replace(p.Search) + "%"
		pattern = &like
	}

	// count(*) OVER() drops the total on an empty page (offset past the end), so
	// the total comes from a separate count that the paged select can't distort.
	var total int
	if err := s.db.QueryRow(ctx, memberEntriesCTE+"\nSELECT count(*) FROM entries"+memberSearchWhere, orgID, p.Status, pattern).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("organization: count member entries org %s: %w", orgID, err)
	}

	q := memberEntriesCTE + `
SELECT status, user_id, invitation_id, email, preferred_name, given_names, last_name,
       role, job_title, department_id, department_name, expires_at, invited_by
FROM entries` + memberSearchWhere + "\nORDER BY " + memberOrderBy(p.Sort, p.Desc) + "\nLIMIT $4 OFFSET $5"

	rows, err := s.db.Query(ctx, q, orgID, p.Status, pattern, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("organization: list member entries org %s: %w", orgID, err)
	}
	defer rows.Close()

	entries := []MemberEntry{}
	for rows.Next() {
		var e MemberEntry
		if err := rows.Scan(&e.Status, &e.UserID, &e.InvitationID, &e.Email, &e.PreferredName,
			&e.GivenNames, &e.LastName, &e.Role, &e.JobTitle, &e.DepartmentID, &e.DepartmentName,
			&e.ExpiresAt, &e.InvitedBy); err != nil {
			return nil, 0, fmt.Errorf("organization: list member entries scan: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("organization: list member entries rows: %w", err)
	}

	return entries, total, nil
}

func (s *Store) GetMember(ctx context.Context, orgID, userID uuid.UUID) (Member, error) {
	const q = `
		SELECT u.id, u.email, u.preferred_name, u.given_names, u.last_name,
		       m.role, m.job_title, m.department_id, d.name
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		LEFT JOIN departments d ON d.id = m.department_id
		WHERE m.organization_id = $1 AND m.user_id = $2`
	var m Member
	err := s.db.QueryRow(ctx, q, orgID, userID).Scan(&m.UserID, &m.Email, &m.PreferredName, &m.GivenNames, &m.LastName,
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
	var oldJobTitle, oldDeptName *string
	err = tx.QueryRow(ctx, `
		SELECT m.role, m.job_title, d.name
		FROM memberships m
		LEFT JOIN departments d ON d.id = m.department_id
		WHERE m.organization_id = $1 AND m.user_id = $2`, orgID, userID).
		Scan(&current, &oldJobTitle, &oldDeptName)
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

	const q = `UPDATE memberships SET role = COALESCE($3, role), job_title = $4, department_id = $5
		WHERE organization_id = $1 AND user_id = $2
		RETURNING (SELECT name FROM departments WHERE id = $5 AND organization_id = $1)`
	var newDeptName *string
	err = tx.QueryRow(ctx, q, orgID, userID, role, jobTitle, departmentID).Scan(&newDeptName)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation && pgErr.ConstraintName == membershipDepartmentFK {
		return Member{}, ErrDepartmentNotFound
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Member{}, ErrNotMember
	}
	if err != nil {
		return Member{}, fmt.Errorf("organization: update membership user %s org %s: %w", userID, orgID, err)
	}

	newRole := current
	if role != nil {
		newRole = *role
	}
	if err := s.audit.Record(ctx, tx, audit.MembershipRoleChanged,
		audit.Target{Type: audit.TargetMembership, ID: userID.String(), OrgID: &orgID},
		audit.Updated(
			map[string]any{"role": current, "jobTitle": oldJobTitle, "department": oldDeptName},
			map[string]any{"role": newRole, "jobTitle": jobTitle, "department": newDeptName})); err != nil {
		return Member{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Member{}, fmt.Errorf("organization: commit update membership user %s org %s: %w", userID, orgID, err)
	}
	return s.GetMember(ctx, orgID, userID)
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
