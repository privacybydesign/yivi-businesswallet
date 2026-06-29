package organization

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

const (
	inviteTTL              = 7 * 24 * time.Hour
	inviteTokenBytes       = 32
	invitationDepartmentFK = "invitations_department_fkey"
)

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
