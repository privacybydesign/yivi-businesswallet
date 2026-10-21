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
// a change. The right outlasts a role change, but only a member's counts: an
// admin reviews as an admin (MayReview).
func (s *Store) SetReviewRight(ctx context.Context, orgID, userID uuid.UUID, right ReviewRight) (Member, error) {
	if right != ReviewRightGranted && right != ReviewRightNone {
		return Member{}, fmt.Errorf("organization: unknown review right %q", right)
	}
	canReview := right == ReviewRightGranted

	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `
			WITH old AS (SELECT can_review FROM memberships WHERE organization_id = $1 AND user_id = $2 FOR UPDATE)
			UPDATE memberships m SET can_review = $3
			FROM old WHERE m.organization_id = $1 AND m.user_id = $2
			RETURNING old.can_review`
		var was bool
		err := q.QueryRow(ctx, update, orgID, userID, canReview).Scan(&was)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotMember
		}
		if err != nil {
			return fmt.Errorf("organization: set review right user %s org %s: %w", userID, orgID, err)
		}
		if was == canReview {
			return nil
		}
		return s.audit.Record(ctx, q, audit.MembershipReviewChanged,
			audit.Target{Type: audit.TargetMembership, ID: userID.String(), OrgID: &orgID},
			audit.Updated(map[string]any{"canReview": was}, map[string]any{"canReview": canReview}))
	})
	if err != nil {
		return Member{}, err
	}
	return s.GetMember(ctx, orgID, userID)
}
