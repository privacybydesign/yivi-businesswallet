package organization

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// VogReminderCandidate is one membership the screening scheduler owes a mail:
// either its first "expiring soon" reminder (Overdue false) or a repeat expired
// reminder (Overdue true), mirroring ReminderCandidate for identity (#242 §6).
type VogReminderCandidate struct {
	UserID  uuid.UUID
	Email   string
	DueAt   time.Time
	Overdue bool
}

// VogReminderCandidates selects the members of orgID who are due a VOG
// reminder right now, under settings' schedule - the same shape
// IdentityReminderCandidates uses over vog_valid_until instead of
// identity_due_at.
func (s *Store) VogReminderCandidates(ctx context.Context, orgID uuid.UUID, settings ScreeningSettings) ([]VogReminderCandidate, error) {
	const q = `
		SELECT m.user_id, u.email, m.vog_valid_until, m.vog_valid_until <= now() AS overdue
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		WHERE m.organization_id = $1
		  AND m.vog_valid_until IS NOT NULL
		  AND m.vog_reminder_count < $2
		  AND (
		        (m.vog_valid_until <= now()
		           AND (m.vog_last_reminder_at IS NULL
		                OR m.vog_last_reminder_at <= now() - make_interval(days => $3::int)))
		     OR (m.vog_valid_until > now()
		           AND m.vog_valid_until <= now() + make_interval(days => $4::int)
		           AND m.vog_last_reminder_at IS NULL)
		      )
		ORDER BY m.vog_valid_until`
	rows, err := s.db.Query(ctx, q, orgID, settings.OverdueReminderMaxCount, settings.OverdueReminderIntervalDays, settings.LookaheadDays())
	if err != nil {
		return nil, fmt.Errorf("organization: vog reminder candidates org %s: %w", orgID, err)
	}
	defer rows.Close()

	var out []VogReminderCandidate
	for rows.Next() {
		var c VogReminderCandidate
		if err := rows.Scan(&c.UserID, &c.Email, &c.DueAt, &c.Overdue); err != nil {
			return nil, fmt.Errorf("organization: vog reminder candidates scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization: vog reminder candidates rows: %w", err)
	}
	return out, nil
}

// RecordVogReminderSent bumps a membership's reminder cadence tracking and
// audits the send, mirroring RecordIdentityReminderSent.
func (s *Store) RecordVogReminderSent(ctx context.Context, orgID, userID uuid.UUID, email string, overdue bool) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE memberships SET vog_last_reminder_at = now(), vog_reminder_count = vog_reminder_count + 1
			WHERE organization_id = $1 AND user_id = $2`
		if _, err := q.Exec(ctx, update, orgID, userID); err != nil {
			return fmt.Errorf("organization: record vog reminder sent user %s org %s: %w", userID, orgID, err)
		}
		action := audit.MembershipVogReminderSent
		if overdue {
			action = audit.MembershipVogExpired
		}
		return s.audit.Record(ctx, q, action,
			audit.Target{Type: audit.TargetMembership, ID: userID.String(), OrgID: &orgID},
			audit.Created(map[string]any{"email": email, "overdue": overdue}))
	})
}
