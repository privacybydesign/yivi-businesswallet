-- +goose Up
-- request_id references identity_proofing_requests ON DELETE SET NULL: deleting
-- a request looks up its deliveries, a sequential scan of the outbox without this.
CREATE INDEX identity_proofing_webhook_deliveries_request_idx
    ON identity_proofing_webhook_deliveries (request_id) WHERE request_id IS NOT NULL;

-- +goose Down
DROP INDEX identity_proofing_webhook_deliveries_request_idx;
