-- +goose Up
CREATE TABLE qerds_messages
(
    id                       UUID        PRIMARY KEY DEFAULT uuidv7(),
    organization_id          UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    direction                TEXT        NOT NULL CHECK (direction IN ('outbound', 'inbound')),
    sender_address           TEXT        NOT NULL,
    recipient_address        TEXT        NOT NULL,
    subject                  TEXT        NOT NULL,
    body                     TEXT        NOT NULL DEFAULT '',
    -- Correlation key into the provider; NULL until an outbound submission is
    -- accepted. UNIQUE (NULLs allowed) so inbound intake dedupes on it.
    provider_ref             TEXT        UNIQUE,
    status                   TEXT        NOT NULL,
    submitted_at             TIMESTAMPTZ,
    delivered_at             TIMESTAMPTZ,
    qualified_timestamp_send TIMESTAMPTZ,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_qerds_messages_organization_id ON qerds_messages (organization_id);

-- +goose Down
DROP TABLE qerds_messages;
