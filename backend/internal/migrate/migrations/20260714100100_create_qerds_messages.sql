-- +goose Up
CREATE TABLE qerds_messages
(
    id                       UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id          UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    direction                TEXT        NOT NULL CHECK (direction IN ('outbound', 'inbound')),
    sender_address           TEXT        NOT NULL,
    recipient_address        TEXT        NOT NULL,
    subject                  TEXT        NOT NULL,
    body                     TEXT        NOT NULL DEFAULT '',
    -- Correlation key into the provider; NULL until an outbound submission is
    -- accepted. Uniqueness is per direction (see the index below), not global.
    provider_ref             TEXT,
    status                   TEXT        NOT NULL,
    submitted_at             TIMESTAMPTZ,
    delivered_at             TIMESTAMPTZ,
    qualified_timestamp_send TIMESTAMPTZ,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_qerds_messages_organization_id ON qerds_messages (organization_id);

-- provider_ref is unique PER DIRECTION, not globally: within a single deployment
-- an outbound message and the looped-back / self-sent inbound copy legitimately
-- share the same provider ref (sender and recipient would be separate DBs in a
-- real cross-party exchange). Repeated inbound intake (poll/webhook retries)
-- still dedupes on (direction, provider_ref). NULLs are allowed and distinct, so
-- unsent outbound rows don't collide.
CREATE UNIQUE INDEX idx_qerds_messages_direction_provider_ref ON qerds_messages (direction, provider_ref);

-- +goose Down
DROP TABLE qerds_messages;
