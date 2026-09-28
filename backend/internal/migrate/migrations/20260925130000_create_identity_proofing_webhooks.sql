-- +goose Up
-- identity_proofing_webhooks is a customer's one webhook endpoint: the wallet
-- POSTs a signed event there when a session of the customer is verified,
-- fails, expires or has its personal data purged, for the event types in
-- events. The signing secret is sealed with IDENTITY_PROOFING_ENCRYPTION_KEY
-- (it has to be readable to sign); secret_last4 is shown to tell secrets apart.
CREATE TABLE identity_proofing_webhooks
(
    customer_id       UUID PRIMARY KEY,
    organization_id   UUID        NOT NULL,
    url               TEXT        NOT NULL,
    events            TEXT[]      NOT NULL,
    secret_ciphertext BYTEA       NOT NULL,
    secret_last4      TEXT        NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (organization_id, customer_id)
        REFERENCES identity_proofing_customers (organization_id, id) ON DELETE CASCADE
);

-- identity_proofing_webhook_deliveries is the outbox: one row per event per
-- customer, written in the same transaction as the change it reports, and sent
-- by the delivery worker with exponential back-off until it succeeds or runs
-- out of attempts. payload holds no personal data: the session id, outcome
-- and assurance only.
CREATE TABLE identity_proofing_webhook_deliveries
(
    id               UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    organization_id  UUID        NOT NULL,
    customer_id      UUID        NOT NULL,
    event            TEXT        NOT NULL,
    request_id       UUID        REFERENCES identity_proofing_requests (id) ON DELETE SET NULL,
    payload          JSONB       NOT NULL,
    status           TEXT        NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'delivered', 'failed')),
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
DROP TABLE identity_proofing_webhooks;
