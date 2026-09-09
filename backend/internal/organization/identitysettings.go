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

const identitySettingsColumns = `employee_interval_months, external_interval_months, reminder_days_before,
	overdue_reminder_interval_days, overdue_reminder_max_count, credential_max_age_days, overdue_consequence, updated_at`

func scanIdentitySettings(row pgx.Row) (IdentitySettings, error) {
	var s IdentitySettings
	err := row.Scan(&s.EmployeeIntervalMonths, &s.ExternalIntervalMonths, &s.ReminderDaysBefore,
		&s.OverdueReminderIntervalDays, &s.OverdueReminderMaxCount, &s.CredentialMaxAgeDays, &s.OverdueConsequence, &s.UpdatedAt)
	return s, err
}

// identitySettingsTx reads an org's identity settings on q, so a caller already
// inside a transaction (accept, recompute) sees a consistent snapshot rather than
// a second round trip on the pool. Configured is false and every field its
// documented default when no row exists — the feature is off, not defaulted to
// some interval no admin chose.
func identitySettingsTx(ctx context.Context, q database.Querier, orgID uuid.UUID) (IdentitySettings, error) {
	row := q.QueryRow(ctx, `SELECT `+identitySettingsColumns+` FROM org_identity_settings WHERE organization_id = $1`, orgID)
	s, err := scanIdentitySettings(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return IdentitySettings{
			Configured:                  false,
			OverdueReminderIntervalDays: 7,
			OverdueReminderMaxCount:     4,
			OverdueConsequence:          OverdueConsequenceFlag,
		}, nil
	}
	if err != nil {
		return IdentitySettings{}, fmt.Errorf("organization: identity settings org %s: %w", orgID, err)
	}
	s.Configured = true
	return s, nil
}

// GetIdentitySettings returns an org's re-identification policy (Configured
// false when never saved).
func (s *Store) GetIdentitySettings(ctx context.Context, orgID uuid.UUID) (IdentitySettings, error) {
	return identitySettingsTx(ctx, s.db, orgID)
}

func validateIdentitySettingsInput(in IdentitySettingsInput) error {
	for _, months := range []*int{in.EmployeeIntervalMonths, in.ExternalIntervalMonths} {
		if months != nil && *months <= 0 {
			return fmt.Errorf("%w: interval months must be positive", ErrIdentitySettingsInvalid)
		}
	}
	if in.CredentialMaxAgeDays != nil && *in.CredentialMaxAgeDays <= 0 {
		return fmt.Errorf("%w: credential max age days must be positive", ErrIdentitySettingsInvalid)
	}
	if in.OverdueReminderIntervalDays <= 0 || in.OverdueReminderMaxCount <= 0 {
		return fmt.Errorf("%w: overdue reminder interval and max count must be positive", ErrIdentitySettingsInvalid)
	}
	for _, d := range in.ReminderDaysBefore {
		if d <= 0 {
			return fmt.Errorf("%w: reminder days must be positive", ErrIdentitySettingsInvalid)
		}
	}
	if in.OverdueConsequence != OverdueConsequenceFlag && in.OverdueConsequence != OverdueConsequenceBlock {
		return fmt.Errorf("%w: overdue consequence must be %q or %q", ErrIdentitySettingsInvalid, OverdueConsequenceFlag, OverdueConsequenceBlock)
	}
	return nil
}

// SaveIdentitySettings upserts an org's re-identification policy, recomputes
// every member's identity_due_at under the new policy, and audits the change —
// all in one transaction, so a policy change and the due dates it implies are
// never observed out of step.
func (s *Store) SaveIdentitySettings(ctx context.Context, orgID uuid.UUID, in IdentitySettingsInput) (IdentitySettings, error) {
	if err := validateIdentitySettingsInput(in); err != nil {
		return IdentitySettings{}, err
	}
	reminderDays := in.ReminderDaysBefore
	if reminderDays == nil {
		reminderDays = []int32{}
	}

	var out IdentitySettings
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := identitySettingsTx(ctx, q, orgID)
		if err != nil {
			return err
		}

		const upsert = `INSERT INTO org_identity_settings
			(organization_id, employee_interval_months, external_interval_months, reminder_days_before,
			 overdue_reminder_interval_days, overdue_reminder_max_count, credential_max_age_days, overdue_consequence)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (organization_id) DO UPDATE SET
				employee_interval_months = EXCLUDED.employee_interval_months,
				external_interval_months = EXCLUDED.external_interval_months,
				reminder_days_before = EXCLUDED.reminder_days_before,
				overdue_reminder_interval_days = EXCLUDED.overdue_reminder_interval_days,
				overdue_reminder_max_count = EXCLUDED.overdue_reminder_max_count,
				credential_max_age_days = EXCLUDED.credential_max_age_days,
				overdue_consequence = EXCLUDED.overdue_consequence,
				updated_at = now()`
		if _, err := q.Exec(ctx, upsert, orgID, in.EmployeeIntervalMonths, in.ExternalIntervalMonths, reminderDays,
			in.OverdueReminderIntervalDays, in.OverdueReminderMaxCount, in.CredentialMaxAgeDays, in.OverdueConsequence); err != nil {
			return fmt.Errorf("organization: save identity settings org %s: %w", orgID, err)
		}

		after, err := identitySettingsTx(ctx, q, orgID)
		if err != nil {
			return err
		}
		if err := recomputeIdentityDueDatesTx(ctx, q, orgID, after); err != nil {
			return err
		}

		if err := s.audit.Record(ctx, q, audit.IdentitySettingsUpdated,
			audit.Target{Type: audit.TargetIdentitySettings, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(identitySettingsAuditSnapshot(before), identitySettingsAuditSnapshot(after))); err != nil {
			return err
		}
		out = after
		return nil
	})
	return out, err
}

func identitySettingsAuditSnapshot(s IdentitySettings) map[string]any {
	return map[string]any{
		"employeeIntervalMonths":      s.EmployeeIntervalMonths,
		"externalIntervalMonths":      s.ExternalIntervalMonths,
		"reminderDaysBefore":          s.ReminderDaysBefore,
		"overdueReminderIntervalDays": s.OverdueReminderIntervalDays,
		"overdueReminderMaxCount":     s.OverdueReminderMaxCount,
		"credentialMaxAgeDays":        s.CredentialMaxAgeDays,
		"overdueConsequence":          s.OverdueConsequence,
	}
}

// dueAtFor computes a membership's next identity_due_at from when it was last
// verified and the policy's interval for its member type, nil when that type's
// interval is off (or the membership has never verified).
func dueAtFor(verifiedAt *time.Time, memberType string, settings IdentitySettings) *time.Time {
	interval := settings.IntervalFor(memberType)
	if verifiedAt == nil || interval == nil {
		return nil
	}
	due := verifiedAt.AddDate(0, *interval, 0)
	return &due
}

// recomputeIdentityDueDatesTx recalculates identity_due_at for every membership
// in an org under settings, run inside the same transaction as a settings save
// (or an accept — see AcceptInvitation) so due dates are never stale relative to
// the policy that produced them.
func recomputeIdentityDueDatesTx(ctx context.Context, q database.Querier, orgID uuid.UUID, settings IdentitySettings) error {
	const update = `
		UPDATE memberships SET identity_due_at = CASE
			WHEN identity_verified_at IS NULL THEN NULL
			WHEN member_type = 'external' AND $2::int IS NOT NULL THEN identity_verified_at + ($2::int || ' months')::interval
			WHEN member_type = 'external' THEN NULL
			WHEN $3::int IS NOT NULL THEN identity_verified_at + ($3::int || ' months')::interval
			ELSE NULL
		END
		WHERE organization_id = $1`
	if _, err := q.Exec(ctx, update, orgID, settings.ExternalIntervalMonths, settings.EmployeeIntervalMonths); err != nil {
		return fmt.Errorf("organization: recompute identity due dates org %s: %w", orgID, err)
	}
	return nil
}

// IdentityBlockedSet returns the set of member user ids who are currently
// refused credential issuance and signing under the org's overdue-block policy
// (#240 §3): OverdueConsequence is "block" and the member's derived status is
// "overdue". An org with the default "flag only" consequence (or no policy at
// all) never blocks, so this returns an empty set without touching the
// memberships table.
func (s *Store) IdentityBlockedSet(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]bool, error) {
	settings, err := s.GetIdentitySettings(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if settings.OverdueConsequence != OverdueConsequenceBlock {
		return map[uuid.UUID]bool{}, nil
	}

	const q = `SELECT user_id, identity_verified_at, identity_due_at, identity_requested_at
		FROM memberships WHERE organization_id = $1`
	rows, err := s.db.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("organization: identity blocked set org %s: %w", orgID, err)
	}
	defer rows.Close()

	now := time.Now()
	lookahead := settings.LookaheadDays()
	blocked := map[uuid.UUID]bool{}
	for rows.Next() {
		var userID uuid.UUID
		var verifiedAt, dueAt, requestedAt *time.Time
		if err := rows.Scan(&userID, &verifiedAt, &dueAt, &requestedAt); err != nil {
			return nil, fmt.Errorf("organization: identity blocked set scan: %w", err)
		}
		if DeriveIdentityStatus(verifiedAt, dueAt, requestedAt, now, lookahead) == IdentityStatusOverdue {
			blocked[userID] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization: identity blocked set rows: %w", err)
	}
	return blocked, nil
}

// IsIdentityBlocked reports whether one member is currently refused credential
// issuance under the org's overdue-block policy. Used by the attestation
// issuance gate, where only a single member's status is at stake.
func (s *Store) IsIdentityBlocked(ctx context.Context, orgID, userID uuid.UUID) (bool, error) {
	settings, err := s.GetIdentitySettings(ctx, orgID)
	if err != nil {
		return false, err
	}
	if settings.OverdueConsequence != OverdueConsequenceBlock {
		return false, nil
	}

	const q = `SELECT identity_verified_at, identity_due_at, identity_requested_at
		FROM memberships WHERE organization_id = $1 AND user_id = $2`
	var verifiedAt, dueAt, requestedAt *time.Time
	err = s.db.QueryRow(ctx, q, orgID, userID).Scan(&verifiedAt, &dueAt, &requestedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("organization: is identity blocked user %s org %s: %w", userID, orgID, err)
	}
	status := DeriveIdentityStatus(verifiedAt, dueAt, requestedAt, time.Now(), settings.LookaheadDays())
	return status == IdentityStatusOverdue, nil
}
