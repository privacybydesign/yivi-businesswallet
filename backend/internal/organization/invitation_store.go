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
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
)

const (
	inviteTTL              = 7 * 24 * time.Hour
	inviteTokenBytes       = 32
	invitationDepartmentFK = "invitations_department_fkey"
)

func newInviteToken() (string, [sha256.Size]byte, error) {
	b := make([]byte, inviteTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", [sha256.Size]byte{}, fmt.Errorf("organization: invite token: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	return raw, sha256.Sum256([]byte(raw)), nil
}

func hashInviteToken(raw string) [sha256.Size]byte {
	return sha256.Sum256([]byte(raw))
}

func (s *Store) CreateInvitation(ctx context.Context, in Invitation) (Invitation, error) {
	rawToken, tokenHash, err := newInviteToken()
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
	if err != nil {
		return Invitation{}, err
	}
	in.Token = rawToken
	return in, nil
}

func (s *Store) InvitationByToken(ctx context.Context, rawToken string) (Invitation, error) {
	hash := hashInviteToken(rawToken)
	const q = `
		SELECT i.id, i.organization_id, o.name, o.slug, i.email, i.invited_by, i.role, i.job_title,
		       i.department_id, d.name, i.invited_given_names, i.invited_last_name, i.expires_at, i.created_at
		FROM invitations i
		JOIN organizations o ON o.id = i.organization_id
		LEFT JOIN departments d ON d.id = i.department_id
		WHERE i.invite_token_hash = $1`
	var inv Invitation
	err := s.db.QueryRow(ctx, q, hash[:]).Scan(&inv.ID, &inv.OrganizationID, &inv.OrganizationName, &inv.OrganizationSlug,
		&inv.Email, &inv.InvitedBy, &inv.Role, &inv.JobTitle, &inv.DepartmentID, &inv.DepartmentName,
		&inv.GivenNames, &inv.LastName, &inv.ExpiresAt, &inv.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invitation{}, ErrInvitationNotFound
	}
	if err != nil {
		return Invitation{}, fmt.Errorf("organization: invitation by token: %w", err)
	}
	return inv, nil
}

func (s *Store) AcceptInvitation(ctx context.Context, inv Invitation, userID uuid.UUID, disclosed identity.Name) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `
			INSERT INTO memberships (organization_id, user_id, role, job_title, department_id)
			VALUES ($1, $2, $3, $4, (SELECT id FROM departments WHERE id = $5 AND organization_id = $1))`
		_, err := q.Exec(ctx, insert, inv.OrganizationID, userID, inv.Role, inv.JobTitle, inv.DepartmentID)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return ErrAlreadyMember
		}
		if err != nil {
			return fmt.Errorf("organization: accept membership user %s org %s: %w", userID, inv.OrganizationID, err)
		}
		if _, err := q.Exec(ctx, `DELETE FROM invitations WHERE id = $1`, inv.ID); err != nil {
			return fmt.Errorf("organization: accept delete invitation %s: %w", inv.ID, err)
		}
		return s.audit.Record(ctx, q, audit.MembershipAccepted,
			audit.Target{Type: audit.TargetMembership, ID: userID.String(), OrgID: &inv.OrganizationID},
			audit.Created(map[string]any{
				"email":      inv.Email,
				"role":       inv.Role,
				"jobTitle":   inv.JobTitle,
				"department": inv.DepartmentName,
				"givenNames": disclosed.GivenNames,
				"lastName":   disclosed.LastName,
			}))
	})
}

func (s *Store) DeclineInvitation(ctx context.Context, rawToken string) error {
	hash := hashInviteToken(rawToken)
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const del = `DELETE FROM invitations WHERE invite_token_hash = $1
			RETURNING organization_id, email, role, invited_given_names, invited_last_name`
		var orgID uuid.UUID
		var email, role, givenNames, lastName string
		err := q.QueryRow(ctx, del, hash[:]).Scan(&orgID, &email, &role, &givenNames, &lastName)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvitationNotFound
		}
		if err != nil {
			return fmt.Errorf("organization: decline invitation: %w", err)
		}
		return s.audit.Record(ctx, q, audit.MembershipDeclined,
			audit.Target{Type: audit.TargetMembership, ID: email, OrgID: &orgID},
			audit.Deleted(map[string]any{
				"email":      email,
				"role":       role,
				"givenNames": givenNames,
				"lastName":   lastName,
			}))
	})
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
	_, tokenHash, err := newInviteToken()
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
