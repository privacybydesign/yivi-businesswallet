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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

const (
	inviteTTL              = 7 * 24 * time.Hour
	inviteTokenBytes       = 32
	invitationDepartmentFK = "invitations_department_fkey"
)

func newInviteTokenHash() ([sha256.Size]byte, error) {
	b := make([]byte, inviteTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("organization: invite token: %w", err)
	}
	return sha256.Sum256([]byte(base64.RawURLEncoding.EncodeToString(b))), nil
}

func (s *Store) CreateInvitation(ctx context.Context, in Invitation) (Invitation, error) {
	tokenHash, err := newInviteTokenHash()
	if err != nil {
		return Invitation{}, err
	}

	err = database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `
			INSERT INTO invitations
				(organization_id, email, invited_by, role, job_title, department_id,
				 invited_given_names, invited_last_name, invite_token_hash, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			RETURNING id, expires_at, created_at,
			          (SELECT name FROM departments WHERE id = $6 AND organization_id = $1)`
		var deptName *string
		err := q.QueryRow(ctx, insert,
			in.OrganizationID, in.Email, in.InvitedBy, in.Role, in.JobTitle, in.DepartmentID,
			in.GivenNames, in.LastName, tokenHash[:], time.Now().Add(inviteTTL),
		).Scan(&in.ID, &in.ExpiresAt, &in.CreatedAt, &deptName)
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
			audit.Created(map[string]any{
				"email":      in.Email,
				"role":       in.Role,
				"givenNames": in.GivenNames,
				"lastName":   in.LastName,
				"jobTitle":   in.JobTitle,
				"department": deptName,
			}))
	})
	return in, err
}

func (s *Store) RevokeInvitation(ctx context.Context, orgID, invitationID uuid.UUID) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const del = `DELETE FROM invitations WHERE id = $1 AND organization_id = $2
			RETURNING email, role, invited_given_names, invited_last_name`
		var email, role, givenNames, lastName string
		err := q.QueryRow(ctx, del, invitationID, orgID).Scan(&email, &role, &givenNames, &lastName)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvitationNotFound
		}
		if err != nil {
			return fmt.Errorf("organization: revoke invitation %s org %s: %w", invitationID, orgID, err)
		}
		return s.audit.Record(ctx, q, audit.MembershipInviteRevoked,
			audit.Target{Type: audit.TargetMembership, ID: email, OrgID: &orgID},
			audit.Deleted(map[string]any{
				"email":      email,
				"role":       role,
				"givenNames": givenNames,
				"lastName":   lastName,
			}))
	})
}

func (s *Store) ResendInvitation(ctx context.Context, orgID, invitationID uuid.UUID) error {
	tokenHash, err := newInviteTokenHash()
	if err != nil {
		return err
	}
	newExpiry := time.Now().Add(inviteTTL)
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `
			WITH old AS (SELECT expires_at FROM invitations WHERE id = $1 AND organization_id = $2)
			UPDATE invitations i SET invite_token_hash = $3, expires_at = $4
			FROM old WHERE i.id = $1 AND i.organization_id = $2
			RETURNING i.email, old.expires_at`
		var email string
		var oldExpiry time.Time
		err := q.QueryRow(ctx, update, invitationID, orgID, tokenHash[:], newExpiry).Scan(&email, &oldExpiry)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvitationNotFound
		}
		if err != nil {
			return fmt.Errorf("organization: resend invitation %s org %s: %w", invitationID, orgID, err)
		}
		return s.audit.Record(ctx, q, audit.MembershipInviteResent,
			audit.Target{Type: audit.TargetMembership, ID: email, OrgID: &orgID},
			audit.Updated(
				map[string]any{"expiresAt": oldExpiry},
				map[string]any{"expiresAt": newExpiry}))
	})
}
