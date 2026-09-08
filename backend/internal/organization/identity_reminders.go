package organization

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// ReminderCandidate is one membership the identity scheduler owes a mail: either
// its first "due soon" reminder (Overdue false) or a repeat overdue reminder
// (Overdue true), per the org's reminder schedule (#240 §6).
type ReminderCandidate struct {
	UserID  uuid.UUID
	Email   string
	DueAt   time.Time
	Overdue bool
}

// IdentityReminderCandidates selects the members of orgID who are due a
// reminder right now, under settings' schedule:
//
//   - overdue (identity_due_at has passed): repeats every
//     OverdueReminderIntervalDays, gated by identity_last_reminder_at;
//   - due soon (within settings.LookaheadDays() of identity_due_at, not yet
//     passed): exactly one reminder, sent the first time the member enters that
//     window (identity_last_reminder_at still NULL) — a deliberate
//     simplification of the fuller "one mail per configured threshold" model:
//     one advance warning plus the repeating overdue cadence covers the same
//     member behaviour (act before the deadline, or be reminded until you do)
//     with one counter instead of tracking which of several thresholds already
//     fired.
//
// Both branches stop once identity_reminder_count reaches
// OverdueReminderMaxCount, so a member is not paged forever.
func (s *Store) IdentityReminderCandidates(ctx context.Context, orgID uuid.UUID, settings IdentitySettings) ([]ReminderCandidate, error) {
	const q = `
		SELECT m.user_id, u.email, m.identity_due_at, m.identity_due_at <= now() AS overdue
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		WHERE m.organization_id = $1
		  AND m.identity_due_at IS NOT NULL
		  AND m.identity_reminder_count < $2
		  AND (
		        (m.identity_due_at <= now()
		           AND (m.identity_last_reminder_at IS NULL
		                OR m.identity_last_reminder_at <= now() - make_interval(days => $3::int)))
		     OR (m.identity_due_at > now()
		           AND m.identity_due_at <= now() + make_interval(days => $4::int)
		           AND m.identity_last_reminder_at IS NULL)
		      )
		ORDER BY m.identity_due_at`
	rows, err := s.db.Query(ctx, q, orgID, settings.OverdueReminderMaxCount, settings.OverdueReminderIntervalDays, settings.LookaheadDays())
	if err != nil {
		return nil, fmt.Errorf("organization: identity reminder candidates org %s: %w", orgID, err)
	}
	defer rows.Close()

	var out []ReminderCandidate
	for rows.Next() {
		var c ReminderCandidate
		if err := rows.Scan(&c.UserID, &c.Email, &c.DueAt, &c.Overdue); err != nil {
			return nil, fmt.Errorf("organization: identity reminder candidates scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization: identity reminder candidates rows: %w", err)
	}
	return out, nil
}

// RecordIdentityReminderSent bumps a membership's reminder cadence tracking
// (identity_last_reminder_at, identity_reminder_count) and audits the send —
// membership.identity_overdue for a repeat overdue reminder,
// membership.identity_reminder_sent for the one due-soon reminder — so a
// restart of the scheduler never double-sends (the next candidate query no
// longer selects this member until the next threshold).
func (s *Store) RecordIdentityReminderSent(ctx context.Context, orgID, userID uuid.UUID, email string, overdue bool) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE memberships SET identity_last_reminder_at = now(), identity_reminder_count = identity_reminder_count + 1
			WHERE organization_id = $1 AND user_id = $2`
		if _, err := q.Exec(ctx, update, orgID, userID); err != nil {
			return fmt.Errorf("organization: record identity reminder sent user %s org %s: %w", userID, orgID, err)
		}
		action := audit.MembershipIdentityReminderSent
		if overdue {
			action = audit.MembershipIdentityOverdue
		}
		return s.audit.Record(ctx, q, action,
			audit.Target{Type: audit.TargetMembership, ID: userID.String(), OrgID: &orgID},
			audit.Created(map[string]any{"email": email, "overdue": overdue}))
	})
}
