-- +goose Up
-- Interim address book: until the European Digital Directory exists, recipients
-- are saved per organization (name -> digital address) for reuse in Compose.
CREATE TABLE qerds_contacts
(
    id              UUID        PRIMARY KEY DEFAULT uuidv7(),
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name            TEXT        NOT NULL,
    address         TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One entry per address within an organization.
    UNIQUE (organization_id, address)
);

CREATE INDEX idx_qerds_contacts_organization_id ON qerds_contacts (organization_id);

-- +goose Down
DROP TABLE qerds_contacts;
