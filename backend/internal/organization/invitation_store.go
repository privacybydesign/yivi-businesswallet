package organization

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

const (
	inviteTTL              = 7 * 24 * time.Hour
	inviteTokenBytes       = 32
	invitationDepartmentFK = "invitations_department_fkey"
)

func (s *Store) ListInvitations(ctx context.Context, orgID uuid.UUID) ([]Invitation, error) {
	const q = `
		SELECT i.id, i.organization_id, i.email, i.invited_by, i.role, i.job_title,
		       i.department_id, d.name, i.invited_given_names, i.invited_last_name,
		       i.expires_at, i.created_at
		FROM invitations i
		LEFT JOIN departments d ON d.id = i.department_id
		WHERE i.organization_id = $1
		ORDER BY i.invited_last_name, i.invited_given_names`
	rows, err := s.db.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("organization: list invitations org %s: %w", orgID, err)
	}
	defer rows.Close()

	invitations := []Invitation{}
	for rows.Next() {
		var inv Invitation
		if err := rows.Scan(&inv.ID, &inv.OrganizationID, &inv.Email, &inv.InvitedBy, &inv.Role,
			&inv.JobTitle, &inv.DepartmentID, &inv.DepartmentName, &inv.GivenNames, &inv.LastName,
			&inv.ExpiresAt, &inv.CreatedAt); err != nil {
			return nil, fmt.Errorf("organization: list invitations scan: %w", err)
		}
		invitations = append(invitations, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization: list invitations rows: %w", err)
	}
	return invitations, nil
}

func (s *Store) CreateInvitation(ctx context.Context, in Invitation) (Invitation, error) {
	b := make([]byte, inviteTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return Invitation{}, fmt.Errorf("organization: invite token: %w", err)
	}
	tokenHash := sha256.Sum256([]byte(base64.RawURLEncoding.EncodeToString(b)))

	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `
			INSERT INTO invitations
				(organization_id, email, invited_by, role, job_title, department_id,
				 invited_given_names, invited_last_name, invite_token_hash, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			RETURNING id, expires_at, created_at`
		err := q.QueryRow(ctx, insert,
			in.OrganizationID, in.Email, in.InvitedBy, in.Role, in.JobTitle, in.DepartmentID,
			in.GivenNames, in.LastName, tokenHash[:], time.Now().Add(inviteTTL),
		).Scan(&in.ID, &in.ExpiresAt, &in.CreatedAt)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch {
			case pgErr.Code == uniqueViolation:
				return ErrAlreadyInvited
			case pgErr.Code == foreignKeyViolation && pgErr.ConstraintName == invitationDepartmentFK:
				return ErrDepartmentNotFound
			}
		}
		if err != nil {
			return fmt.Errorf("organization: create invitation %s org %s: %w", in.Email, in.OrganizationID, err)
		}
		return s.audit.Record(ctx, q, audit.MembershipInvited,
			audit.Target{Type: audit.TargetMembership, ID: in.Email, OrgID: &in.OrganizationID},
			map[string]any{"email": in.Email, "role": in.Role})
	})
	return in, err
}
