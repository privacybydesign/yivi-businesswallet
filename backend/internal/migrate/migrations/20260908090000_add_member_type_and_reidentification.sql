-- +goose Up
-- Member re-identification lifecycle (#240), continued from
-- 20260716150000_add_member_identity.sql (which added identity_verified_at and
-- date_of_birth). This slice adds the fields needed to compute a due date and a
-- status from that timestamp, plus the employee/external distinction the org's
-- policy is keyed on.
--
-- member_type is chosen at invite time (default 'employee', the common case) and
-- editable by an admin afterwards; external_organisation is optional free text
-- (who an external works for), relevant only when member_type = 'external'.
--
-- identity_due_at is computed from identity_verified_at plus the org's configured
-- interval for the member's type (org_identity_settings, next migration) and
-- recomputed whenever either changes; NULL means no interval applies (the org has
-- switched re-identification off for that type, or the member has never verified).
-- identity_requested_at / identity_requested_by record an admin's on-demand
-- request (single or bulk); cleared on a successful re-identification.
-- identity_last_reminder_at / identity_reminder_count back the scheduler's
-- idempotent reminder cadence — a restart must not double-send a reminder that
-- already went out today.
ALTER TABLE memberships
    ADD COLUMN member_type              TEXT NOT NULL DEFAULT 'employee' CHECK (member_type IN ('employee', 'external')),
    ADD COLUMN external_organisation    TEXT,
    ADD COLUMN identity_due_at          TIMESTAMPTZ,
    ADD COLUMN identity_requested_at    TIMESTAMPTZ,
    ADD COLUMN identity_requested_by    UUID REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN identity_last_reminder_at TIMESTAMPTZ,
    ADD COLUMN identity_reminder_count  INT NOT NULL DEFAULT 0;

-- The scheduler's daily pass selects members whose due date has crossed a
-- reminder threshold; NULLS are the common case (no policy, or never verified)
-- and are pruned from the partial index rather than bloating it.
CREATE INDEX idx_memberships_identity_due_at ON memberships (identity_due_at) WHERE identity_due_at IS NOT NULL;

-- Invitations carry the same intended member_type/external_organisation, applied
-- to the membership on accept (mirrors how role/job_title/department already
-- flow from invitation to membership).
ALTER TABLE invitations
    ADD COLUMN member_type           TEXT NOT NULL DEFAULT 'employee' CHECK (member_type IN ('employee', 'external')),
    ADD COLUMN external_organisation TEXT;

-- +goose Down
ALTER TABLE invitations
    DROP COLUMN member_type,
    DROP COLUMN external_organisation;

DROP INDEX idx_memberships_identity_due_at;

ALTER TABLE memberships
    DROP COLUMN member_type,
    DROP COLUMN external_organisation,
    DROP COLUMN identity_due_at,
    DROP COLUMN identity_requested_at,
    DROP COLUMN identity_requested_by,
    DROP COLUMN identity_last_reminder_at,
    DROP COLUMN identity_reminder_count;
