-- +goose Up
CREATE TABLE qerds_addresses
(
    id              UUID        PRIMARY KEY DEFAULT uuidv7(),
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    address         TEXT        NOT NULL UNIQUE,
    is_default      BOOLEAN     NOT NULL DEFAULT false,
    provider_ref    TEXT,
    provisioned_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_qerds_addresses_organization_id ON qerds_addresses (organization_id);

-- At most one default digital address per organization (Art 6(1)(j) allows >= 1).
CREATE UNIQUE INDEX idx_qerds_addresses_one_default ON qerds_addresses (organization_id) WHERE is_default;

-- +goose Down
DROP TABLE qerds_addresses;
