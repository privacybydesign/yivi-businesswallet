-- +goose Up
-- org_identity_settings is an org's re-identification policy (#240): how often
-- employees and externals must re-prove their identity, the reminder schedule,
-- how old a credential may be at re-identification, and what happens when a
-- member goes overdue. One row per org, upserted like org_notification_settings;
-- Configured is false (in Go) when no row exists yet, in which case the feature
-- is off (no due dates, no reminders) rather than defaulting to some interval no
-- admin chose.
--
-- employee_interval_months / external_interval_months: NULL = off (that member
-- type never becomes due). Separate values because externals are typically
-- re-checked more often (#240 §3).
-- reminder_days_before: days before identity_due_at to send a reminder, e.g.
-- {30,14,7}; the largest value is also what the "due soon" status uses as its
-- lookahead window.
-- overdue_reminder_interval_days / overdue_reminder_max_count: cadence and cap
-- for repeat reminders once overdue, so a member is not paged forever.
-- credential_max_age_days: NULL = off. When set, a re-identification whose
-- disclosed credential's issuer `iat` is older than this is rejected (#240 §5).
-- overdue_consequence: 'flag' (default, status-only) or 'block' (also refuses
-- credential issuance and signing as a signer — see internal/organization's
-- identity gate).
CREATE TABLE org_identity_settings
(
    organization_id               UUID PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    employee_interval_months      INT,
    external_interval_months      INT,
    reminder_days_before          INT[]       NOT NULL DEFAULT '{30,14,7}',
    overdue_reminder_interval_days INT        NOT NULL DEFAULT 7,
    overdue_reminder_max_count    INT         NOT NULL DEFAULT 4,
    credential_max_age_days       INT,
    overdue_consequence           TEXT        NOT NULL DEFAULT 'flag' CHECK (overdue_consequence IN ('flag', 'block')),
    created_at                    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (employee_interval_months IS NULL OR employee_interval_months > 0),
    CHECK (external_interval_months IS NULL OR external_interval_months > 0),
    CHECK (credential_max_age_days IS NULL OR credential_max_age_days > 0),
    CHECK (overdue_reminder_interval_days > 0),
    CHECK (overdue_reminder_max_count > 0)
);

-- +goose Down
DROP TABLE org_identity_settings;
