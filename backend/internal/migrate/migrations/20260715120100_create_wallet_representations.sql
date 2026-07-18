-- +goose Up
-- The mandate list (Art 5(1)(j), Art 6(2)) for an organization/wallet.
CREATE TABLE wallet_representations
(
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id    UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
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
    -- sole/jointly: bestuurder authority; beperkt/volledig: volmacht scope.
    CONSTRAINT wallet_representations_authority_check CHECK (authority IN ('sole', 'jointly', 'beperkt', 'volledig'))
);

CREATE INDEX idx_wallet_representations_organization ON wallet_representations (organization_id);

-- +goose Down
DROP TABLE wallet_representations;
