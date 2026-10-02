-- +goose Up
-- A customer-API POST sent with an Idempotency-Key: the first answer is kept
-- for 24 hours and replayed to a retry with the same key and body.
-- status_code is NULL while the first call is still running.
CREATE TABLE identity_proofing_idempotency_keys
(
    customer_id  UUID        NOT NULL REFERENCES identity_proofing_customers (id) ON DELETE CASCADE,
    key          TEXT        NOT NULL,
    request_hash BYTEA       NOT NULL,
    status_code  INTEGER,
    response     BYTEA,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, key)
);

CREATE INDEX identity_proofing_idempotency_keys_created_idx
    ON identity_proofing_idempotency_keys (created_at);

-- +goose Down
DROP TABLE identity_proofing_idempotency_keys;
