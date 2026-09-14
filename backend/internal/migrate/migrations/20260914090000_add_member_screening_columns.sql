-- +goose Up
-- Member screening / VOG (#242): the membership-side bookkeeping the screening
-- lifecycle needs, mirroring the shape #240 added for re-identification
-- (20260908090000_add_member_type_and_reidentification.sql).
--
-- The member list reads a member's screening status from these columns alone,
-- never from member_screenings directly, so listing a page of members never
-- joins screening history per row:
--   - vog_last_result mirrors the *latest* member_screenings.result, whichever
--     it is - so the list can tell "never checked" (NULL) apart from "checked,
--     but the latest attempt did not pass" (a non-valid result) without a join.
--   - vog_valid_until / vog_covered_codes additionally mirror valid_until /
--     covered_codes of that latest attempt, but only when it passed (result =
--     'valid'); otherwise both are NULL, by the same "latest attempt wins" rule
--     DeriveScreeningStatus (internal/organization) applies: a fresh failed
--     re-check overrides an older still-unexpired valid one, deliberately, so
--     the status always reflects the most recent evidence.
-- All three are written by the same transaction that records a
-- member_screenings row (RecordScreening) and recomputed, for vog_valid_until,
-- when the org's re-check interval or anchor changes (recomputeScreeningTx),
-- exactly like identity_due_at.
--
-- vog_requested_at / vog_requested_by record an admin's on-demand "request VOG"
-- (single or bulk), cleared on the next recorded screening attempt.
-- vog_last_reminder_at / vog_reminder_count back the scheduler's idempotent
-- reminder cadence, exactly like their identity_* counterparts.
ALTER TABLE memberships
    ADD COLUMN vog_last_result       TEXT CHECK (vog_last_result IN ('valid', 'rejected', 'mismatch', 'insufficient_scope')),
    ADD COLUMN vog_valid_until       TIMESTAMPTZ,
    ADD COLUMN vog_covered_codes     TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN vog_requested_at      TIMESTAMPTZ,
    ADD COLUMN vog_requested_by      UUID REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN vog_last_reminder_at  TIMESTAMPTZ,
    ADD COLUMN vog_reminder_count    INT NOT NULL DEFAULT 0;

CREATE INDEX idx_memberships_vog_valid_until ON memberships (vog_valid_until) WHERE vog_valid_until IS NOT NULL;

-- +goose Down
DROP INDEX idx_memberships_vog_valid_until;

ALTER TABLE memberships
    DROP COLUMN vog_last_result,
    DROP COLUMN vog_valid_until,
    DROP COLUMN vog_covered_codes,
    DROP COLUMN vog_requested_at,
    DROP COLUMN vog_requested_by,
    DROP COLUMN vog_last_reminder_at,
    DROP COLUMN vog_reminder_count;
