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

const screeningSettingsColumns = `required_for, required_codes, max_age_at_upload_days,
	employee_recheck_interval_months, external_recheck_interval_months, recheck_anchor, reminder_days_before,
	overdue_reminder_interval_days, overdue_reminder_max_count, overdue_consequence, accept_yivi_credential, updated_at`

func scanScreeningSettings(row pgx.Row) (ScreeningSettings, error) {
	var s ScreeningSettings
	err := row.Scan(&s.RequiredFor, &s.RequiredCodes, &s.MaxAgeAtUploadDays,
		&s.EmployeeRecheckIntervalMonths, &s.ExternalRecheckIntervalMonths, &s.RecheckAnchor, &s.ReminderDaysBefore,
		&s.OverdueReminderIntervalDays, &s.OverdueReminderMaxCount, &s.OverdueConsequence, &s.AcceptYiviCredential, &s.UpdatedAt)
	return s, err
}

// screeningSettingsTx reads an org's screening settings on q, mirroring
// identitySettingsTx: Configured is false and every field its documented
// default when no row exists - the feature is off (RequiredFor "nobody"), not
// defaulting to a requirement no admin chose.
func screeningSettingsTx(ctx context.Context, q database.Querier, orgID uuid.UUID) (ScreeningSettings, error) {
	row := q.QueryRow(ctx, `SELECT `+screeningSettingsColumns+` FROM org_screening_settings WHERE organization_id = $1`, orgID)
	s, err := scanScreeningSettings(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ScreeningSettings{
			Configured:                  false,
			RequiredFor:                 ScreeningRequiredForNobody,
			RequiredCodes:               []string{},
			RecheckAnchor:               RecheckAnchorIssueDate,
			ReminderDaysBefore:          []int32{},
			OverdueReminderIntervalDays: 7,
			OverdueReminderMaxCount:     4,
			OverdueConsequence:          OverdueConsequenceFlag,
		}, nil
	}
	if err != nil {
		return ScreeningSettings{}, fmt.Errorf("organization: screening settings org %s: %w", orgID, err)
	}
	s.Configured = true
	return s, nil
}

// GetScreeningSettings returns an org's VOG policy (Configured false when never
// saved).
func (s *Store) GetScreeningSettings(ctx context.Context, orgID uuid.UUID) (ScreeningSettings, error) {
	return screeningSettingsTx(ctx, s.db, orgID)
}

var validRequiredFor = map[string]bool{
	ScreeningRequiredForNobody: true, ScreeningRequiredForEmployees: true,
	ScreeningRequiredForExternals: true, ScreeningRequiredForBoth: true,
}

var validRecheckAnchor = map[string]bool{RecheckAnchorIssueDate: true, RecheckAnchorCheckedAt: true}

func validateScreeningSettingsInput(in ScreeningSettingsInput) error {
	if !validRequiredFor[in.RequiredFor] {
		return fmt.Errorf("%w: requiredFor must be one of nobody/employees/externals/both", ErrScreeningSettingsInvalid)
	}
	if !validRecheckAnchor[in.RecheckAnchor] {
		return fmt.Errorf("%w: recheckAnchor must be issue_date or checked_at", ErrScreeningSettingsInvalid)
	}
	for _, months := range []*int{in.EmployeeRecheckIntervalMonths, in.ExternalRecheckIntervalMonths} {
		if months != nil && *months <= 0 {
			return fmt.Errorf("%w: recheck interval months must be positive", ErrScreeningSettingsInvalid)
		}
	}
	if in.MaxAgeAtUploadDays != nil && *in.MaxAgeAtUploadDays <= 0 {
		return fmt.Errorf("%w: max age at upload days must be positive", ErrScreeningSettingsInvalid)
	}
	if in.OverdueReminderIntervalDays <= 0 || in.OverdueReminderMaxCount <= 0 {
		return fmt.Errorf("%w: overdue reminder interval and max count must be positive", ErrScreeningSettingsInvalid)
	}
	for _, d := range in.ReminderDaysBefore {
		if d <= 0 {
			return fmt.Errorf("%w: reminder days must be positive", ErrScreeningSettingsInvalid)
		}
	}
	if in.OverdueConsequence != OverdueConsequenceFlag && in.OverdueConsequence != OverdueConsequenceBlock {
		return fmt.Errorf("%w: overdue consequence must be %q or %q", ErrScreeningSettingsInvalid, OverdueConsequenceFlag, OverdueConsequenceBlock)
	}
	for _, code := range in.RequiredCodes {
		if !twoDigitCode(code) {
			return fmt.Errorf("%w: required code %q must be two digits", ErrScreeningSettingsInvalid, code)
		}
	}
	return nil
}

func twoDigitCode(s string) bool {
	if len(s) != 2 {
		return false
	}
	return s[0] >= '0' && s[0] <= '9' && s[1] >= '0' && s[1] <= '9'
}

// SaveScreeningSettings upserts an org's VOG policy, recomputes every member's
// vog_valid_until under the new recheck interval/anchor, and audits the change
// - all in one transaction, mirroring SaveIdentitySettings.
func (s *Store) SaveScreeningSettings(ctx context.Context, orgID uuid.UUID, in ScreeningSettingsInput) (ScreeningSettings, error) {
	if err := validateScreeningSettingsInput(in); err != nil {
		return ScreeningSettings{}, err
	}
	requiredCodes := in.RequiredCodes
	if requiredCodes == nil {
		requiredCodes = []string{}
	}
	reminderDays := in.ReminderDaysBefore
	if reminderDays == nil {
		reminderDays = []int32{}
	}

	var out ScreeningSettings
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := screeningSettingsTx(ctx, q, orgID)
		if err != nil {
			return err
		}

		const upsert = `INSERT INTO org_screening_settings
			(organization_id, required_for, required_codes, max_age_at_upload_days,
			 employee_recheck_interval_months, external_recheck_interval_months, recheck_anchor, reminder_days_before,
			 overdue_reminder_interval_days, overdue_reminder_max_count, overdue_consequence, accept_yivi_credential)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (organization_id) DO UPDATE SET
				required_for = EXCLUDED.required_for,
				required_codes = EXCLUDED.required_codes,
				max_age_at_upload_days = EXCLUDED.max_age_at_upload_days,
				employee_recheck_interval_months = EXCLUDED.employee_recheck_interval_months,
				external_recheck_interval_months = EXCLUDED.external_recheck_interval_months,
				recheck_anchor = EXCLUDED.recheck_anchor,
				reminder_days_before = EXCLUDED.reminder_days_before,
				overdue_reminder_interval_days = EXCLUDED.overdue_reminder_interval_days,
				overdue_reminder_max_count = EXCLUDED.overdue_reminder_max_count,
				overdue_consequence = EXCLUDED.overdue_consequence,
				accept_yivi_credential = EXCLUDED.accept_yivi_credential,
				updated_at = now()`
		if _, err := q.Exec(ctx, upsert, orgID, in.RequiredFor, requiredCodes, in.MaxAgeAtUploadDays,
			in.EmployeeRecheckIntervalMonths, in.ExternalRecheckIntervalMonths, in.RecheckAnchor, reminderDays,
			in.OverdueReminderIntervalDays, in.OverdueReminderMaxCount, in.OverdueConsequence, in.AcceptYiviCredential); err != nil {
			return fmt.Errorf("organization: save screening settings org %s: %w", orgID, err)
		}

		after, err := screeningSettingsTx(ctx, q, orgID)
		if err != nil {
			return err
		}
		if err := recomputeScreeningValidUntilTx(ctx, q, orgID, after); err != nil {
			return err
		}

		if err := s.audit.Record(ctx, q, audit.ScreeningSettingsUpdated,
			audit.Target{Type: audit.TargetScreeningSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(screeningSettingsAuditSnapshot(before), screeningSettingsAuditSnapshot(after))); err != nil {
			return err
		}
		out = after
		return nil
	})
	return out, err
}

func screeningSettingsAuditSnapshot(s ScreeningSettings) map[string]any {
	return map[string]any{
		"requiredFor":                   s.RequiredFor,
		"requiredCodes":                 s.RequiredCodes,
		"maxAgeAtUploadDays":            s.MaxAgeAtUploadDays,
		"employeeRecheckIntervalMonths": s.EmployeeRecheckIntervalMonths,
		"externalRecheckIntervalMonths": s.ExternalRecheckIntervalMonths,
		"recheckAnchor":                 s.RecheckAnchor,
		"reminderDaysBefore":            s.ReminderDaysBefore,
		"overdueReminderIntervalDays":   s.OverdueReminderIntervalDays,
		"overdueReminderMaxCount":       s.OverdueReminderMaxCount,
		"overdueConsequence":            s.OverdueConsequence,
		"acceptYiviCredential":          s.AcceptYiviCredential,
	}
}

// recomputeScreeningValidUntilTx recalculates vog_valid_until for every member
// of orgID whose latest attempt passed, under settings' recheck interval and
// anchor, run inside the same transaction as a settings save so a member's
// expiry is never stale relative to the policy that produced it. A member whose
// latest attempt did not pass (vog_last_result NULL or non-valid) is untouched
// - NULL either way, per DeriveScreeningStatus's "latest attempt wins" rule.
func recomputeScreeningValidUntilTx(ctx context.Context, q database.Querier, orgID uuid.UUID, settings ScreeningSettings) error {
	// The anchor date (issue date or check date, per settings.RecheckAnchor) of a
	// membership's latest screening, correlated to the UPDATE target - a scalar
	// subquery, not a FROM-list LATERAL join, because only a correlated subquery
	// may reference the UPDATE's own target table.
	const anchor = `(SELECT CASE WHEN $4::text = 'checked_at' THEN ms.checked_at ELSE ms.vog_issue_date::timestamptz END
		FROM member_screenings ms WHERE ms.organization_id = m.organization_id AND ms.user_id = m.user_id
		ORDER BY ms.checked_at DESC LIMIT 1)`
	update := `
		UPDATE memberships m SET vog_valid_until = CASE
			WHEN m.vog_last_result IS DISTINCT FROM 'valid' THEN NULL
			WHEN m.member_type = 'external' AND $2::int IS NOT NULL THEN ` + anchor + ` + ($2::int || ' months')::interval
			WHEN m.member_type = 'external' THEN NULL
			WHEN $3::int IS NOT NULL THEN ` + anchor + ` + ($3::int || ' months')::interval
			ELSE NULL
		END
		WHERE m.organization_id = $1`
	if _, err := q.Exec(ctx, update, orgID, settings.ExternalRecheckIntervalMonths, settings.EmployeeRecheckIntervalMonths, settings.RecheckAnchor); err != nil {
		return fmt.Errorf("organization: recompute screening valid-until org %s: %w", orgID, err)
	}
	return nil
}

// vogDueAtFor computes a screening's valid_until from its anchor date (issue
// date or check date, per settings) and the configured re-check interval for a
// member type, nil when off.
func vogDueAtFor(anchor time.Time, memberType string, settings ScreeningSettings) *time.Time {
	interval := settings.RecheckIntervalFor(memberType)
	if interval == nil {
		return nil
	}
	due := anchor.AddDate(0, *interval, 0)
	return &due
}
