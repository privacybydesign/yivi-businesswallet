-- +goose Up
-- identity_proofing_api_keys are a customer's machine credentials: its backend
-- creates and reads proofing sessions for the customer with one, through the
-- public /proofing API, instead of a member sending them. Only the SHA-256 of
-- a key is kept; the key is shown once, at creation. prefix is its public
-- beginning, shown to tell keys apart. A revoked key stays listed and no
-- longer authenticates. Removing the customer removes its keys.
CREATE TABLE identity_proofing_api_keys
(
    id              UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL,
    customer_id     UUID        NOT NULL,
    name            TEXT        NOT NULL CHECK (btrim(name) <> ''),
    prefix          TEXT        NOT NULL,
    secret_hash     BYTEA       NOT NULL UNIQUE,
    created_by      UUID        REFERENCES users (id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    FOREIGN KEY (organization_id, customer_id)
        REFERENCES identity_proofing_customers (organization_id, id) ON DELETE CASCADE
);

CREATE INDEX identity_proofing_api_keys_customer_idx
    ON identity_proofing_api_keys (customer_id, created_at);

-- A request created through the API names the key instead of a member
-- (requested_by stays NULL).
ALTER TABLE identity_proofing_requests
    ADD COLUMN api_key_id UUID REFERENCES identity_proofing_api_keys (id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE identity_proofing_requests
    DROP COLUMN api_key_id;
DROP TABLE identity_proofing_api_keys;
