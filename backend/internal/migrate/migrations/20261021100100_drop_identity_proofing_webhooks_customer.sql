-- +goose Up
-- An org has one webhook endpoint, which receives the events of all its flows.
-- An org with several keeps the one of its oldest customer that had one.
DELETE
FROM identity_proofing_webhooks w
WHERE w.customer_id <> (SELECT w2.customer_id
                        FROM identity_proofing_webhooks w2
                                 JOIN identity_proofing_customers c ON c.id = w2.customer_id
                        WHERE w2.organization_id = w.organization_id
                        ORDER BY c.created_at, c.id
                        LIMIT 1);

-- Dropping the column drops the primary key and the composite foreign key.
ALTER TABLE identity_proofing_webhooks
    DROP COLUMN customer_id,
    ADD PRIMARY KEY (organization_id),
    ADD FOREIGN KEY (organization_id) REFERENCES organizations (id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE identity_proofing_webhooks
    DROP CONSTRAINT identity_proofing_webhooks_pkey,
    DROP CONSTRAINT identity_proofing_webhooks_organization_id_fkey,
    ADD COLUMN customer_id UUID;

-- The customers table is recreated, one per org, by the migration that drops it.
UPDATE identity_proofing_webhooks w
SET customer_id = (SELECT c.id FROM identity_proofing_customers c WHERE c.organization_id = w.organization_id)
WHERE w.customer_id IS NULL;

ALTER TABLE identity_proofing_webhooks
    ALTER COLUMN customer_id SET NOT NULL,
    ADD PRIMARY KEY (customer_id),
    ADD FOREIGN KEY (organization_id, customer_id)
        REFERENCES identity_proofing_customers (organization_id, id) ON DELETE CASCADE;
