package organization

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// ReviewRight is the membership's reviewer right as a typed value.
func (m Membership) ReviewRight() ReviewRight {
	if m.CanReview {
		return ReviewRightGranted
	}
	return ReviewRightNone
}

// SetReviewRight gives a member the reviewer right or takes it away, and audits
// a change. An admin reviews by their role (MayReview), so setting it on one is
// ErrAdminReviews: a right stored on an admin would come back with a demotion
// nobody gave it for. A role change clears it (UpdateMembership).
func (s *Store) SetReviewRight(ctx context.Context, orgID, userID uuid.UUID, right ReviewRight) (Member, error) {
	if right != ReviewRightGranted && right != ReviewRightNone {
		return Member{}, fmt.Errorf("organization: unknown review right %q", right)
	}
	canReview := right == ReviewRightGranted

	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		var role string
		var was bool
		err := q.QueryRow(ctx, `SELECT role, can_review FROM memberships
			WHERE organization_id = $1 AND user_id = $2 FOR UPDATE`, orgID, userID).Scan(&role, &was)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotMember
		}
		if err != nil {
			return fmt.Errorf("organization: read review right user %s org %s: %w", userID, orgID, err)
		}
		if role != RoleMember {
			return ErrAdminReviews
		}
		if was == canReview {
			return nil
		}
		if _, err := q.Exec(ctx, `UPDATE memberships SET can_review = $3 WHERE organization_id = $1 AND user_id = $2`,
			orgID, userID, canReview); err != nil {
			return fmt.Errorf("organization: set review right user %s org %s: %w", userID, orgID, err)
		}
		return s.recordReviewChanged(ctx, q, orgID, userID, was, canReview)
	})
	if err != nil {
		return Member{}, err
	}
	return s.GetMember(ctx, orgID, userID)
}

// recordReviewChanged audits the reviewer right going from was to now.
func (s *Store) recordReviewChanged(ctx context.Context, q database.Querier, orgID, userID uuid.UUID, was, now bool) error {
	return s.audit.Record(ctx, q, audit.MembershipReviewChanged,
		audit.Target{Type: audit.TargetMembership, ID: userID.String(), OrgID: &orgID},
		audit.Updated(map[string]any{"canReview": was}, map[string]any{"canReview": now}))
}
