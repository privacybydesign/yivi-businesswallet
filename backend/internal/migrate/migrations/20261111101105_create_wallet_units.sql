-- +goose Up
-- A business wallet unit (CS-05 BWU): the wallet service instance the provider
-- attests. organization_id is set null, not cascaded, when the org is deleted, so
-- the status entries of its attestations stay published as invalid until they expire.
CREATE TABLE wallet_units
(
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        REFERENCES organizations (id) ON DELETE SET NULL,
    status          TEXT        NOT NULL DEFAULT 'active',
    revoked_reason  TEXT,
    revoked_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT wallet_units_status_check CHECK (status IN ('active', 'revoked')),
    CONSTRAINT wallet_units_revoked_check CHECK ((status = 'revoked') = (revoked_at IS NOT NULL))
);

CREATE UNIQUE INDEX wallet_units_one_active_per_org ON wallet_units (organization_id) WHERE status = 'active';
CREATE INDEX wallet_units_organization_id ON wallet_units (organization_id, created_at DESC);

-- +goose Down
DROP TABLE wallet_units;
