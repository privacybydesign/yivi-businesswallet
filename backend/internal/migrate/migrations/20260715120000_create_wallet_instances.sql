-- +goose Up
CREATE TABLE wallet_instances
(
    id                     UUID        PRIMARY KEY DEFAULT uuidv7(),
    status                 TEXT        NOT NULL,
    requestor_user_id      UUID        REFERENCES users (id) ON DELETE SET NULL,
    kvk_number             TEXT        NOT NULL,
    digital_address        TEXT        NOT NULL UNIQUE,
    organization_id        UUID        UNIQUE REFERENCES organizations (id) ON DELETE CASCADE,
    legal_name             TEXT,
    euid                   TEXT,
    request_message_id     UUID        REFERENCES qerds_messages (id) ON DELETE SET NULL,
    attestation_message_id UUID        REFERENCES qerds_messages (id) ON DELETE SET NULL,
    reject_reason          TEXT,
    bootstrapped_at        TIMESTAMPTZ,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Lifecycle: provisioning -> awaiting_attestation -> active | rejected; active -> suspended | revoked.
    CONSTRAINT wallet_instances_status_check CHECK (
        status IN ('provisioning', 'awaiting_attestation', 'active', 'rejected', 'suspended', 'revoked')
    )
);

CREATE INDEX idx_wallet_instances_requestor ON wallet_instances (requestor_user_id);

-- At most one in-flight registration per requester + KVK number (a rejected or
-- active instance does not block a fresh attempt).
CREATE UNIQUE INDEX idx_wallet_instances_live ON wallet_instances (requestor_user_id, kvk_number)
    WHERE status IN ('provisioning', 'awaiting_attestation');

-- +goose Down
DROP TABLE wallet_instances;
