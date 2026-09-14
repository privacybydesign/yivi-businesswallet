-- +goose Up
-- org_screening_settings is an org's VOG policy (#242): who needs one, which
-- function-aspect / specific-profile codes it must cover, how old it may be at
-- upload, the re-check interval, and what happens once a member is overdue.
-- One row per org, upserted like org_identity_settings; no row means the
-- feature has never been configured, which Go treats as required_for "nobody" -
-- off, not defaulting to a requirement no admin chose.
--
-- required_for: who the org screens; "nobody" is the shipped default.
-- required_codes: the function-aspect and/or specific-profile codes (see
-- internal/vog.FunctionAspects for the aspect subset) a member's VOG must
-- cover, evaluated against a screening record's covered_codes at check time -
-- see member_screenings below.
-- max_age_at_upload_days: NULL = off. When set, a VOG whose issue date is older
-- than this at upload is rejected.
-- employee_recheck_interval_months / external_recheck_interval_months: NULL =
-- off for that member type, mirroring org_identity_settings.
-- recheck_anchor: whether the re-check interval counts from the VOG's own issue
-- date (default - #242's "computed from the VOG issue date (default) or from
-- the check date") or from when it was checked.
-- accept_yivi_credential: opt-in to the pbdf.vog credential disclosure path
-- alongside the always-on PDF upload; off by default because it carries the
-- liability trade-off #242 requires the org to accept knowingly (the credential
-- is issued by Stichting Privacy by Design about a PDF it validated at
-- issuance, not a live Justis statement).
CREATE TABLE org_screening_settings
(
    organization_id                   UUID PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    required_for                      TEXT        NOT NULL DEFAULT 'nobody' CHECK (required_for IN ('nobody', 'employees', 'externals', 'both')),
    required_codes                    TEXT[]      NOT NULL DEFAULT '{}',
    max_age_at_upload_days            INT,
    employee_recheck_interval_months  INT,
    external_recheck_interval_months  INT,
    recheck_anchor                    TEXT        NOT NULL DEFAULT 'issue_date' CHECK (recheck_anchor IN ('issue_date', 'checked_at')),
    reminder_days_before              INT[]       NOT NULL DEFAULT '{30,14,7}',
    overdue_reminder_interval_days    INT         NOT NULL DEFAULT 7,
    overdue_reminder_max_count        INT         NOT NULL DEFAULT 4,
    overdue_consequence               TEXT        NOT NULL DEFAULT 'flag' CHECK (overdue_consequence IN ('flag', 'block')),
    accept_yivi_credential            BOOLEAN     NOT NULL DEFAULT false,
    created_at                        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (max_age_at_upload_days IS NULL OR max_age_at_upload_days > 0),
    CHECK (employee_recheck_interval_months IS NULL OR employee_recheck_interval_months > 0),
    CHECK (external_recheck_interval_months IS NULL OR external_recheck_interval_months > 0),
    CHECK (overdue_reminder_interval_days > 0),
    CHECK (overdue_reminder_max_count > 0)
);

-- +goose Down
DROP TABLE org_screening_settings;
