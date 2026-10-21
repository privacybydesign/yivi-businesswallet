-- +goose Up
-- Deliveries belong to the org's one endpoint. A delivery still waiting for the
-- endpoint of a customer that lost its endpoint to the org's one would be sent
-- to another customer's URL, so it is dropped.
DELETE
FROM identity_proofing_webhook_deliveries d
WHERE d.status = 'pending'
  AND d.endpoint_url IS DISTINCT FROM
      (SELECT w.url FROM identity_proofing_webhooks w WHERE w.organization_id = d.organization_id);

-- Dropping the column drops the composite foreign keys and the customer index.
-- A delivery's request stays one of its org.
ALTER TABLE identity_proofing_webhook_deliveries
    DROP COLUMN customer_id,
    ADD CONSTRAINT identity_proofing_webhook_deliveries_org_fkey
        FOREIGN KEY (organization_id) REFERENCES organizations (id) ON DELETE CASCADE,
    ADD CONSTRAINT identity_proofing_webhook_deliveries_request_fkey
        FOREIGN KEY (organization_id, request_id)
            REFERENCES identity_proofing_requests (organization_id, id) ON DELETE SET NULL (request_id);

CREATE INDEX identity_proofing_webhook_deliveries_org_idx
    ON identity_proofing_webhook_deliveries (organization_id, created_at DESC);

-- +goose Down
DROP INDEX identity_proofing_webhook_deliveries_org_idx;

ALTER TABLE identity_proofing_webhook_deliveries
    DROP CONSTRAINT identity_proofing_webhook_deliveries_request_fkey,
    DROP CONSTRAINT identity_proofing_webhook_deliveries_org_fkey,
    ADD COLUMN customer_id UUID;

-- The customers table is recreated, one per org, by the migration that drops it.
UPDATE identity_proofing_webhook_deliveries d
SET customer_id = (SELECT c.id FROM identity_proofing_customers c WHERE c.organization_id = d.organization_id)
WHERE d.customer_id IS NULL;

ALTER TABLE identity_proofing_webhook_deliveries
    ALTER COLUMN customer_id SET NOT NULL,
    ADD FOREIGN KEY (organization_id, customer_id)
        REFERENCES identity_proofing_customers (organization_id, id) ON DELETE CASCADE,
    ADD FOREIGN KEY (organization_id, customer_id, request_id)
        REFERENCES identity_proofing_requests (organization_id, customer_id, id) ON DELETE SET NULL (request_id);

CREATE INDEX identity_proofing_webhook_deliveries_customer_idx
    ON identity_proofing_webhook_deliveries (customer_id, created_at DESC);
