-- +goose Up
-- The flow's own retention at send, when it set one: a request's data is kept
-- that long, in place of its customer's data retention.
ALTER TABLE identity_proofing_requests
    ADD COLUMN retention_override_seconds INTEGER CHECK (retention_override_seconds > 0);

-- +goose Down
ALTER TABLE identity_proofing_requests DROP COLUMN retention_override_seconds;
