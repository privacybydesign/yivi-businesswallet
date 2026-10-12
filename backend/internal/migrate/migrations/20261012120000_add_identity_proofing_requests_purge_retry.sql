-- +goose Up
-- A request the purge fails to erase (the engine refuses, or its sealed
-- fields do not open) waits purge_retry_after before the purge takes it
-- again, longer after each of its purge_attempts, so it never holds up the
-- requests due after it.
ALTER TABLE identity_proofing_requests
    ADD COLUMN purge_attempts    INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN purge_retry_after TIMESTAMPTZ;

-- +goose Down
ALTER TABLE identity_proofing_requests
    DROP COLUMN purge_retry_after,
    DROP COLUMN purge_attempts;
