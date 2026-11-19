-- +goose Up
-- An organization acting as OpenID4VP relying party toward another
-- organization's business wallet (issue #271, the sender side): the signed
-- Request Object it serves once from request_uri, the nonce/state its
-- response_uri checks the answer against, and — once the other organization
-- approved — the verified disclosure. The row id is the path segment of both
-- public URLs; state and nonce, not the id, authenticate a response.
CREATE TABLE openid4vp_outbound_requests (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id    UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    created_by         UUID        REFERENCES users (id) ON DELETE SET NULL,
    sender_address     TEXT        NOT NULL,
    recipient_address  TEXT        NOT NULL,
    qerds_message_id   UUID        REFERENCES qerds_messages (id) ON DELETE SET NULL,
    client_id          TEXT        NOT NULL,
    nonce              TEXT        NOT NULL,
    state              TEXT        NOT NULL,
    -- What was asked, [{id, vct, claims}], for display next to the answer.
    credentials        JSONB       NOT NULL,
    request_object     TEXT        NOT NULL,
    request_fetched_at TIMESTAMPTZ,
    status             TEXT        NOT NULL CHECK (status IN ('sent', 'completed', 'failed')),
    failure_reason     TEXT,
    -- The verified presentations, [{queryId, vct, issuer, claims}]; only ever
    -- written together with status = 'completed'.
    disclosed          JSONB,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at         TIMESTAMPTZ NOT NULL,
    responded_at       TIMESTAMPTZ
);

CREATE INDEX idx_openid4vp_outbound_requests_org
    ON openid4vp_outbound_requests (organization_id, created_at DESC);

-- +goose Down
DROP TABLE openid4vp_outbound_requests;
