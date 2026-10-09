-- +goose Up
-- The flows assigned to a customer, with one default: requests for the
-- customer can use these. Like identity_proofing_flow_settings, flow_id names
-- a flow of the proofing provider and has no foreign key; a flow the provider
-- no longer lists is ignored on read.
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

-- +goose Down
DROP TABLE identity_proofing_customer_flows;
