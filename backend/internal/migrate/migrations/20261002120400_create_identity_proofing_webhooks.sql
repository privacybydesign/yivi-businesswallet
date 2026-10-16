-- +goose Up
-- identity_proofing_webhooks is a customer's own webhook endpoint: the wallet
-- POSTs a signed event there when a session of the customer is verified,
-- fails, expires or has its personal data purged, for the event types in
-- events. The signing secret is sealed with IDENTITY_PROOFING_ENCRYPTION_KEY
-- (it has to be readable to sign); secret_last4 is shown to tell secrets apart.
-- A rotated-out secret still signs next to the new one until
-- previous_secret_until, so the customer can switch without missing a
-- delivery. No row means the customer is sent no events.
CREATE TABLE identity_proofing_webhooks
(
    customer_id                UUID PRIMARY KEY,
    organization_id            UUID        NOT NULL,
    url                        TEXT        NOT NULL,
    events                     TEXT[]      NOT NULL,
    secret_ciphertext          BYTEA       NOT NULL,
    secret_last4               TEXT        NOT NULL,
    previous_secret_ciphertext BYTEA,
    previous_secret_until      TIMESTAMPTZ,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (organization_id, customer_id)
        REFERENCES identity_proofing_customers (organization_id, id) ON DELETE CASCADE,
    CHECK ((previous_secret_ciphertext IS NULL) = (previous_secret_until IS NULL))
);

-- +goose Down
DROP TABLE identity_proofing_webhooks;
