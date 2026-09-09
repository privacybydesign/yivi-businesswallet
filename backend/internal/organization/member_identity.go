package organization

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// UpdateMemberType changes a member's type (employee/external) and, for
// external, the optional organisation they work for; recomputes their
// identity_due_at under the org's current policy (the two types can carry
// different intervals) and audits the change.
func (s *Store) UpdateMemberType(ctx context.Context, orgID, userID uuid.UUID, memberType string, externalOrganisation *string) (Member, error) {
	if memberType != MemberTypeEmployee && memberType != MemberTypeExternal {
		return Member{}, fmt.Errorf("%w: member type must be %q or %q", ErrIdentitySettingsInvalid, MemberTypeEmployee, MemberTypeExternal)
	}
	if memberType == MemberTypeEmployee {
		externalOrganisation = nil
	}

	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		settings, err := identitySettingsTx(ctx, q, orgID)
		if err != nil {
			return err
		}

		const update = `
			WITH old AS (SELECT member_type, external_organisation, identity_verified_at FROM memberships WHERE organization_id = $1 AND user_id = $2 FOR UPDATE)
			UPDATE memberships m SET member_type = $3, external_organisation = $4
			FROM old WHERE m.organization_id = $1 AND m.user_id = $2
			RETURNING old.member_type, old.external_organisation, old.identity_verified_at`
		var oldType string
		var oldExternal *string
		var verifiedAt *time.Time
		err = q.QueryRow(ctx, update, orgID, userID, memberType, externalOrganisation).Scan(&oldType, &oldExternal, &verifiedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotMember
		}
		if err != nil {
			return fmt.Errorf("organization: update member type user %s org %s: %w", userID, orgID, err)
		}

		if _, err := q.Exec(ctx, `UPDATE memberships SET identity_due_at = $3 WHERE organization_id = $1 AND user_id = $2`,
			orgID, userID, dueAtFor(verifiedAt, memberType, settings)); err != nil {
			return fmt.Errorf("organization: recompute identity due date user %s org %s: %w", userID, orgID, err)
		}

		if oldType == memberType && strPtrEqual(oldExternal, externalOrganisation) {
			return nil
		}
		return s.audit.Record(ctx, q, audit.MembershipTypeChanged,
			audit.Target{Type: audit.TargetMembership, ID: userID.String(), OrgID: &orgID},
			audit.Updated(
				map[string]any{"memberType": oldType, "externalOrganisation": oldExternal},
				map[string]any{"memberType": memberType, "externalOrganisation": externalOrganisation}))
	})
	if err != nil {
		return Member{}, err
	}
	return s.GetMember(ctx, orgID, userID)
}

func strPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// RequestedMember is one member an admin's "request identification" reached: who
// they are and the fresh re-identification link to mail them.
type RequestedMember struct {
	UserID          uuid.UUID
	Email           string
	ReidentifyToken string
}

// RequestIdentification sets identity_requested_at/by for one or more members
// (single or bulk — #240 §4), mints or rotates each one's re-identification
// token, and audits one event per member, all in one transaction. A user id that
// is not a member of orgID is silently skipped (the caller only ever offers ids
// from that org's own member list, so this guards a race rather than a
// client-facing validation rule).
func (s *Store) RequestIdentification(ctx context.Context, orgID uuid.UUID, userIDs []uuid.UUID, requestedBy uuid.UUID, reason string) ([]RequestedMember, error) {
	var out []RequestedMember
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		now := time.Now()
		for _, userID := range userIDs {
			const update = `
				UPDATE memberships SET identity_requested_at = $3, identity_requested_by = $4
				WHERE organization_id = $1 AND user_id = $2
				RETURNING (SELECT email FROM users WHERE id = $2)`
			var email string
			err := q.QueryRow(ctx, update, orgID, userID, now, requestedBy).Scan(&email)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return fmt.Errorf("organization: request identification user %s org %s: %w", userID, orgID, err)
			}

			token, _, err := ensureReverifyTokenTx(ctx, q, orgID, userID)
			if err != nil {
				return err
			}

			if err := s.audit.Record(ctx, q, audit.MembershipIdentityRequested,
				audit.Target{Type: audit.TargetMembership, ID: userID.String(), OrgID: &orgID},
				audit.Created(map[string]any{"email": email, "reason": nullIfEmpty(reason)})); err != nil {
				return err
			}
			out = append(out, RequestedMember{UserID: userID, Email: email, ReidentifyToken: token})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
