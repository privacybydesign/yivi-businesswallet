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
    -- The owner's standing instruction for their data when this provider stops
    -- serving them (Art 7(6)(f)): hand it over, or hand it over and then erase it.
    -- It is captured in advance because termination is exactly the moment nobody
    -- can be asked.
    data_instruction    TEXT        NOT NULL DEFAULT 'transfer',
    -- When the provider terminated service for this organisation.
    terminated_at       TIMESTAMPTZ,
    -- Set when a termination honoured a 'delete' instruction: the bundle was
    -- produced and handed over, and erasure is now owed. Destruction is a
    -- deliberate operator step, never a side effect of the trigger, so this is a
    -- marker rather than a deletion.
    erasure_pending_at  TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT organizations_status_check CHECK (status IN ('active', 'suspended', 'revoked')),
    CONSTRAINT organizations_data_instruction_check CHECK (data_instruction IN ('transfer', 'delete'))
);

-- +goose Down
DROP TABLE organizations;
