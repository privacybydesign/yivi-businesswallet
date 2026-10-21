-- +goose Up
-- An API key belongs to the org, not to a customer: one key reaches every flow.
-- Dropping the column drops its composite unique key, foreign key and index.
ALTER TABLE identity_proofing_api_keys
    DROP COLUMN customer_id,
    ADD CONSTRAINT identity_proofing_api_keys_org_id_key UNIQUE (organization_id, id),
    ADD CONSTRAINT identity_proofing_api_keys_org_fkey
        FOREIGN KEY (organization_id) REFERENCES organizations (id) ON DELETE CASCADE;

CREATE INDEX identity_proofing_api_keys_org_idx
    ON identity_proofing_api_keys (organization_id, created_at);

-- A request's key stays one of its org.
ALTER TABLE identity_proofing_requests
    ADD CONSTRAINT identity_proofing_requests_api_key_fkey
        FOREIGN KEY (organization_id, api_key_id)
            REFERENCES identity_proofing_api_keys (organization_id, id) ON DELETE SET NULL (api_key_id);

-- +goose Down
ALTER TABLE identity_proofing_requests
    DROP CONSTRAINT identity_proofing_requests_api_key_fkey;

DROP INDEX identity_proofing_api_keys_org_idx;

ALTER TABLE identity_proofing_api_keys
    DROP CONSTRAINT identity_proofing_api_keys_org_fkey,
    DROP CONSTRAINT identity_proofing_api_keys_org_id_key,
    ADD COLUMN customer_id UUID;

-- The customers table is recreated, one per org, by the migration that drops it.
UPDATE identity_proofing_api_keys k
SET customer_id = (SELECT c.id FROM identity_proofing_customers c WHERE c.organization_id = k.organization_id)
WHERE k.customer_id IS NULL;

ALTER TABLE identity_proofing_api_keys
    ALTER COLUMN customer_id SET NOT NULL,
    ADD UNIQUE (organization_id, customer_id, id),
    ADD FOREIGN KEY (organization_id, customer_id)
        REFERENCES identity_proofing_customers (organization_id, id) ON DELETE CASCADE;

CREATE INDEX identity_proofing_api_keys_customer_idx
    ON identity_proofing_api_keys (customer_id, created_at);
