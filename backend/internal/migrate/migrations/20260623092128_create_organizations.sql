-- +goose Up
-- An organization IS a business wallet: the wallet identity (KVK number, EUID,
-- digital address) lives here rather than in a separate wallet_instances table.
CREATE TABLE organizations
(
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT        NOT NULL,          -- the register's official legal name
    slug            TEXT        NOT NULL UNIQUE,
    kvk_number      TEXT        NOT NULL UNIQUE,   -- one wallet per company
    euid            TEXT        NOT NULL,
    digital_address TEXT        NOT NULL UNIQUE,   -- QERDS unique digital address (Art 6(1)(j))
    status          TEXT        NOT NULL DEFAULT 'active',
    bootstrapped_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT organizations_status_check CHECK (status IN ('active', 'suspended', 'revoked'))
);

-- +goose Down
DROP TABLE organizations;
