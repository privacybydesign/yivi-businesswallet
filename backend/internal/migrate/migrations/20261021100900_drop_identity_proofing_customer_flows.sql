-- +goose Up
-- The flows assigned to a customer are gone with it: every flow of the org can
-- be sent. The customer's default became the org's (the migration that adds
-- is_default).
DROP TABLE identity_proofing_customer_flows;

-- +goose Down
CREATE TABLE identity_proofing_customer_flows
(
    organization_id UUID        NOT NULL,
    customer_id     UUID        NOT NULL,
    flow_id         TEXT        NOT NULL,
    is_default      BOOLEAN     NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, flow_id),
    FOREIGN KEY (organization_id, customer_id)
        REFERENCES identity_proofing_customers (organization_id, id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX identity_proofing_customer_flows_one_default_idx
    ON identity_proofing_customer_flows (customer_id) WHERE is_default;

-- Every flow an org holds settings for is assigned to its customer again, with
-- the org's default as the customer's.
INSERT INTO identity_proofing_customer_flows (organization_id, customer_id, flow_id, is_default)
SELECT s.organization_id, c.id, s.flow_id, s.is_default
FROM identity_proofing_flow_settings s
         JOIN identity_proofing_customers c ON c.organization_id = s.organization_id
WHERE s.kind = 'identity';
