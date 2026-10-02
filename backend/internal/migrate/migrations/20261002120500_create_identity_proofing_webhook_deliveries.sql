-- +goose Up
-- identity_proofing_webhook_deliveries is the outbox: one row per session event
-- of a customer, written in the same transaction as the change it reports, and
-- sent by the delivery worker with exponential back-off until it succeeds or
-- runs out of attempts. payload holds no personal data: the session id,
-- outcome and assurance only.
CREATE TABLE identity_proofing_webhook_deliveries
(
    id               UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    organization_id  UUID        NOT NULL,
    customer_id      UUID        NOT NULL,
    event            TEXT        NOT NULL,
    request_id       UUID        REFERENCES identity_proofing_requests (id) ON DELETE SET NULL,
    payload          JSONB       NOT NULL,
    -- The customer's endpoint it goes to; NULL for the wallet's own default.
    endpoint_url     TEXT,
    status           TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'delivered', 'failed')),
    attempts         INTEGER     NOT NULL DEFAULT 0,
    next_attempt_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_attempt_at  TIMESTAMPTZ,
    last_status_code INTEGER,
    last_error       TEXT,
    delivered_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (organization_id, customer_id)
        REFERENCES identity_proofing_customers (organization_id, id) ON DELETE CASCADE
);

CREATE INDEX identity_proofing_webhook_deliveries_due_idx
    ON identity_proofing_webhook_deliveries (next_attempt_at) WHERE status = 'pending';
CREATE INDEX identity_proofing_webhook_deliveries_customer_idx
    ON identity_proofing_webhook_deliveries (customer_id, created_at DESC);

-- +goose Down
DROP TABLE identity_proofing_webhook_deliveries;
