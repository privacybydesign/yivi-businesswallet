-- +goose Up
-- An Idempotency-Key is kept per org, not per customer. The kept answers are a
-- 24-hour cache of what the first call answered, so they are dropped rather than
-- carried over: a retry across this migration runs again.
DELETE FROM identity_proofing_idempotency_keys;

ALTER TABLE identity_proofing_idempotency_keys
    DROP COLUMN customer_id,
    ADD COLUMN organization_id UUID NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    ADD PRIMARY KEY (organization_id, key);

-- +goose Down
DELETE FROM identity_proofing_idempotency_keys;

ALTER TABLE identity_proofing_idempotency_keys
    DROP COLUMN organization_id,
    ADD COLUMN customer_id UUID NOT NULL REFERENCES identity_proofing_customers (id) ON DELETE CASCADE,
    ADD PRIMARY KEY (customer_id, key);
