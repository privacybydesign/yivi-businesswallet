-- +goose Up
-- The session a stored answer names, so that purging the session drops the
-- answer (it carries the subject's details and links) with it.
ALTER TABLE identity_proofing_idempotency_keys
    ADD COLUMN request_id UUID REFERENCES identity_proofing_requests (id) ON DELETE CASCADE;

CREATE INDEX identity_proofing_idempotency_keys_request_idx
    ON identity_proofing_idempotency_keys (request_id);

-- +goose Down
ALTER TABLE identity_proofing_idempotency_keys DROP COLUMN request_id;
