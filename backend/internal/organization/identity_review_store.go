package organization

import (
	"context"
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

type IdentityReview struct {
	ID                  uuid.UUID `json:"id"`
	UserID              uuid.UUID `json:"userId"`
	Email               string    `json:"email"`
	OrganizationName    string    `json:"organizationName"`
	OrganizationSlug    string    `json:"organizationSlug"`
	StoredGivenNames    string    `json:"storedGivenNames"`
	StoredLastName      string    `json:"storedLastName"`
	DisclosedGivenNames string    `json:"disclosedGivenNames"`
	DisclosedLastName   string    `json:"disclosedLastName"`
	CreatedAt           time.Time `json:"createdAt"`
}

type ResolveOutcome struct {
	Approved         bool
	OrganizationSlug string
	OrganizationName string
}

type ReviewState string

const (
	ReviewPending  ReviewState = "pending"
	ReviewRejected ReviewState = "rejected"
)

// CreateIdentityReview holds an accept for review, returning the effective state
// of the review for this invitation. A fresh hold is "pending"; if a review
// already exists its current status is returned, so a re-accept after a
// rejection is reported as rejected rather than mistaken for a new pending hold.
func (s *Store) CreateIdentityReview(ctx context.Context, inv Invitation, userID uuid.UUID, stored, disclosed identity.Name) (ReviewState, error) {
	var state ReviewState
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `
			INSERT INTO identity_reviews
				(user_id, invitation_id, stored_given_names, stored_last_name, disclosed_given_names, disclosed_last_name)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (invitation_id) DO NOTHING
			RETURNING id`
		var id uuid.UUID
		err := q.QueryRow(ctx, insert, userID, inv.ID,
			stored.GivenNames, stored.LastName, disclosed.GivenNames, disclosed.LastName).Scan(&id)
		if err == nil {
			state = ReviewPending
			return s.audit.Record(ctx, q, audit.UserIdentityReviewRequired,
				audit.Target{Type: audit.TargetUser, ID: userID.String(), OrgID: &inv.OrganizationID},
				audit.Updated(
					map[string]any{"givenNames": stored.GivenNames, "lastName": stored.LastName},
					map[string]any{"givenNames": disclosed.GivenNames, "lastName": disclosed.LastName}))
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("organization: create identity review: %w", err)
		}

		var existing string
		if err := q.QueryRow(ctx,
			`SELECT status FROM identity_reviews WHERE invitation_id = $1`, inv.ID).Scan(&existing); err != nil {
			return fmt.Errorf("organization: read existing identity review: %w", err)
		}
		state = ReviewState(existing)
		return nil
	})
	return state, err
}

func (s *Store) ListIdentityReviews(ctx context.Context) ([]IdentityReview, error) {
	const q = `
		SELECT r.id, r.user_id, u.email, o.name, o.slug,
		       r.stored_given_names, r.stored_last_name, r.disclosed_given_names, r.disclosed_last_name, r.created_at
		FROM identity_reviews r
		JOIN users u ON u.id = r.user_id
		JOIN invitations i ON i.id = r.invitation_id
		JOIN organizations o ON o.id = i.organization_id
		WHERE r.status = 'pending'
		ORDER BY r.created_at`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("organization: list identity reviews: %w", err)
	}
	defer rows.Close()

	reviews := []IdentityReview{}
	for rows.Next() {
		var r IdentityReview
		if err := rows.Scan(&r.ID, &r.UserID, &r.Email, &r.OrganizationName, &r.OrganizationSlug,
			&r.StoredGivenNames, &r.StoredLastName, &r.DisclosedGivenNames, &r.DisclosedLastName, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("organization: list identity reviews scan: %w", err)
		}
		reviews = append(reviews, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization: list identity reviews rows: %w", err)
	}
	return reviews, nil
}

func (s *Store) ResolveIdentityReview(ctx context.Context, reviewID, reviewerID uuid.UUID, approve bool) (ResolveOutcome, error) {
	var outcome ResolveOutcome
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		var userID, invitationID uuid.UUID
		var disclosedGiven, disclosedLast, storedGiven, storedLast, status string
		err := q.QueryRow(ctx, `
			SELECT user_id, invitation_id, disclosed_given_names, disclosed_last_name, stored_given_names, stored_last_name, status
			FROM identity_reviews WHERE id = $1 FOR UPDATE`, reviewID).
			Scan(&userID, &invitationID, &disclosedGiven, &disclosedLast, &storedGiven, &storedLast, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrReviewNotFound
		}
		if err != nil {
			return fmt.Errorf("organization: read identity review %s: %w", reviewID, err)
		}
		if status != "pending" {
			return ErrReviewResolved
		}

		if !approve {
			if _, err := q.Exec(ctx, `UPDATE identity_reviews SET status = 'rejected', reviewed_by = $2, reviewed_at = now() WHERE id = $1`,
				reviewID, reviewerID); err != nil {
				return fmt.Errorf("organization: reject identity review %s: %w", reviewID, err)
			}
			return s.audit.Record(ctx, q, audit.UserIdentityReviewRejected,
				audit.Target{Type: audit.TargetUser, ID: userID.String()},
				audit.Deleted(map[string]any{"givenNames": disclosedGiven, "lastName": disclosedLast}))
		}

		var inv Invitation
		err = q.QueryRow(ctx, `
			SELECT i.organization_id, o.name, o.slug, i.email, i.role, i.job_title, i.department_id
			FROM invitations i JOIN organizations o ON o.id = i.organization_id
			WHERE i.id = $1`, invitationID).
			Scan(&inv.OrganizationID, &inv.OrganizationName, &inv.OrganizationSlug, &inv.Email, &inv.Role, &inv.JobTitle, &inv.DepartmentID)
		if err != nil {
			return fmt.Errorf("organization: read held invitation %s: %w", invitationID, err)
		}

		cleaned := identity.Name{GivenNames: disclosedGiven, LastName: disclosedLast}.Clean()
		if _, err := q.Exec(ctx, `UPDATE users SET given_names = $2, last_name = $3, updated_at = now() WHERE id = $1`,
			userID, cleaned.GivenNames, cleaned.LastName); err != nil {
			return fmt.Errorf("organization: approve update user %s: %w", userID, err)
		}

		const insertMembership = `
			INSERT INTO memberships (organization_id, user_id, role, job_title, department_id)
			VALUES ($1, $2, $3, $4, (SELECT id FROM departments WHERE id = $5 AND organization_id = $1))`
		_, err = q.Exec(ctx, insertMembership, inv.OrganizationID, userID, inv.Role, inv.JobTitle, inv.DepartmentID)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return ErrAlreadyMember
		}
		if err != nil {
			return fmt.Errorf("organization: approve membership user %s: %w", userID, err)
		}

		if err := s.audit.Record(ctx, q, audit.UserIdentityReviewApproved,
			audit.Target{Type: audit.TargetUser, ID: userID.String(), OrgID: &inv.OrganizationID},
			audit.Updated(
				map[string]any{"givenNames": storedGiven, "lastName": storedLast},
				map[string]any{"givenNames": disclosedGiven, "lastName": disclosedLast})); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, q, audit.MembershipAccepted,
			audit.Target{Type: audit.TargetMembership, ID: userID.String(), OrgID: &inv.OrganizationID},
			audit.Created(map[string]any{
				"email":      inv.Email,
				"role":       inv.Role,
				"givenNames": disclosedGiven,
				"lastName":   disclosedLast,
			})); err != nil {
			return err
		}

		// Deleting the invitation cascades this review row away; the audit trail
		// above is the durable record of the resolution.
		if _, err := q.Exec(ctx, `DELETE FROM invitations WHERE id = $1`, invitationID); err != nil {
			return fmt.Errorf("organization: approve delete invitation %s: %w", invitationID, err)
		}

		outcome = ResolveOutcome{Approved: true, OrganizationSlug: inv.OrganizationSlug, OrganizationName: inv.OrganizationName}
		return nil
	})
	return outcome, err
}
