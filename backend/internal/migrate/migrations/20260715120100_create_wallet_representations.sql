-- +goose Up
CREATE TABLE wallet_representations
(
    id                 UUID        PRIMARY KEY DEFAULT uuidv7(),
    wallet_instance_id UUID        NOT NULL REFERENCES wallet_instances (id) ON DELETE CASCADE,
    organization_id    UUID        REFERENCES organizations (id) ON DELETE CASCADE,
    kind               TEXT        NOT NULL,
    given_names        TEXT        NOT NULL,
    family_name        TEXT        NOT NULL,
    date_of_birth      DATE,
    authority          TEXT        NOT NULL DEFAULT 'sole',
    valid_from         TIMESTAMPTZ,
    valid_until        TIMESTAMPTZ,
    claimed_by_user_id UUID        REFERENCES users (id) ON DELETE SET NULL,
    claimed_at         TIMESTAMPTZ,
    revoked_at         TIMESTAMPTZ,
    source_message_id  UUID        REFERENCES qerds_messages (id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- bestuurder = director (owner-grade), gevolmachtigde = proxy (scoped).
    CONSTRAINT wallet_representations_kind_check CHECK (kind IN ('bestuurder', 'gevolmachtigde', 'overig')),
    CONSTRAINT wallet_representations_authority_check CHECK (authority IN ('sole', 'jointly'))
);

CREATE INDEX idx_wallet_representations_instance ON wallet_representations (wallet_instance_id);
CREATE INDEX idx_wallet_representations_organization ON wallet_representations (organization_id);

-- +goose Down
DROP TABLE wallet_representations;
