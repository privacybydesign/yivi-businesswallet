-- +goose Up
-- An Idempotency-Key is no longer kept per customer: it is per API key (the
-- migration that adds api_key_id, once the keys are the org's). The kept
-- answers are a 24-hour cache of what the first call answered, so they are
-- dropped rather than carried over: a retry across this migration runs again.
TRUNCATE identity_proofing_idempotency_keys;

-- Dropping the column drops the primary key and the composite foreign keys.
-- The answer's request stays one of its org.
ALTER TABLE identity_proofing_idempotency_keys
    DROP COLUMN customer_id,
    ADD PRIMARY KEY (organization_id, key),
    ADD CONSTRAINT identity_proofing_idempotency_keys_org_fkey
        FOREIGN KEY (organization_id) REFERENCES organizations (id) ON DELETE CASCADE,
    ADD CONSTRAINT identity_proofing_idempotency_keys_request_fkey
        FOREIGN KEY (organization_id, request_id)
            REFERENCES identity_proofing_requests (organization_id, id) ON DELETE CASCADE;

-- +goose Down
TRUNCATE identity_proofing_idempotency_keys;

ALTER TABLE identity_proofing_idempotency_keys
    DROP CONSTRAINT identity_proofing_idempotency_keys_request_fkey,
    DROP CONSTRAINT identity_proofing_idempotency_keys_org_fkey,
    DROP CONSTRAINT identity_proofing_idempotency_keys_pkey,
    ADD COLUMN customer_id UUID NOT NULL,
    ADD PRIMARY KEY (customer_id, key),
    ADD FOREIGN KEY (organization_id, customer_id)
        REFERENCES identity_proofing_customers (organization_id, id) ON DELETE CASCADE,
    ADD FOREIGN KEY (organization_id, customer_id, request_id)
        REFERENCES identity_proofing_requests (organization_id, customer_id, id) ON DELETE CASCADE;
