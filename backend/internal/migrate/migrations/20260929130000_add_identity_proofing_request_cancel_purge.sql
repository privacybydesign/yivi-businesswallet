-- +goose Up
-- A customer can cancel a session that has no outcome yet, and erase one: the
-- request keeps its row, marked, for the audit trail and the API.
ALTER TABLE identity_proofing_requests
    ADD COLUMN cancelled_at TIMESTAMPTZ,
    ADD COLUMN purged_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE identity_proofing_requests
    DROP COLUMN purged_at,
    DROP COLUMN cancelled_at;
