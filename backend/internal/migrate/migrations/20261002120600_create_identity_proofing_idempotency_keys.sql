-- +goose Up
-- A customer-API POST sent with an Idempotency-Key: the first answer is kept
-- for 24 hours and replayed to a retry with the same key and body.
-- status_code is NULL while the first call is still running. request_id is
-- the session the answer names, so purging the session drops the answer (it
-- carries the subject's details and links) with it.
CREATE TABLE identity_proofing_idempotency_keys
(
    organization_id UUID        NOT NULL,
    customer_id     UUID        NOT NULL,
    key             TEXT        NOT NULL,
    request_hash    BYTEA       NOT NULL,
    status_code     INTEGER,
    response        BYTEA,
    request_id      UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, key),
    FOREIGN KEY (organization_id, customer_id)
        REFERENCES identity_proofing_customers (organization_id, id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id, request_id)
        REFERENCES identity_proofing_requests (organization_id, id) ON DELETE CASCADE
);

CREATE INDEX identity_proofing_idempotency_keys_created_idx
    ON identity_proofing_idempotency_keys (created_at);
CREATE INDEX identity_proofing_idempotency_keys_request_idx
    ON identity_proofing_idempotency_keys (request_id);

-- +goose Down
DROP TABLE identity_proofing_idempotency_keys;
