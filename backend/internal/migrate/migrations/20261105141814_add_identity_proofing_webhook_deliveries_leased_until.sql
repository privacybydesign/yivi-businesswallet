-- +goose Up
-- leased_until marks a delivery a worker claimed and is sending, until its
-- outcome is recorded or the lease lapses: claimDue counts these per org, so an
-- endpoint has at most endpointWorkers in flight across replicas.
ALTER TABLE identity_proofing_webhook_deliveries ADD COLUMN IF NOT EXISTS leased_until TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS identity_proofing_webhook_deliveries_leased_idx
    ON identity_proofing_webhook_deliveries (organization_id, leased_until) WHERE status = 'pending' AND leased_until IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS identity_proofing_webhook_deliveries_leased_idx;
ALTER TABLE identity_proofing_webhook_deliveries DROP COLUMN IF EXISTS leased_until;
