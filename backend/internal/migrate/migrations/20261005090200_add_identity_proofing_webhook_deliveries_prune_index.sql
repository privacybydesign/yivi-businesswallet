-- +goose Up
-- PruneDeliveries drops delivered and failed deliveries by age.
CREATE INDEX identity_proofing_webhook_deliveries_prune_idx
    ON identity_proofing_webhook_deliveries (created_at) WHERE status <> 'pending';

-- +goose Down
DROP INDEX identity_proofing_webhook_deliveries_prune_idx;
