-- +goose Up
-- ListPurgeDue reads the unpurged requests whose purge_at passed, earliest
-- first.
CREATE INDEX identity_proofing_requests_purge_due_idx
    ON identity_proofing_requests (purge_at) WHERE purged_at IS NULL;

-- +goose Down
DROP INDEX identity_proofing_requests_purge_due_idx;
