-- +goose Up
-- Each BWIA or SKA the provider issued, with its Token Status List index. The
-- index is never reused: its entry stays published until status_expires_at.
CREATE TABLE wallet_unit_attestations
(
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_unit_id    UUID        NOT NULL REFERENCES wallet_units (id),
    kind              TEXT        NOT NULL,
    status_index      BIGINT      NOT NULL UNIQUE,
    issued_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at        TIMESTAMPTZ NOT NULL,
    status_expires_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT wallet_unit_attestations_kind_check CHECK (kind IN ('bwia', 'ska'))
);

CREATE INDEX wallet_unit_attestations_unit ON wallet_unit_attestations (wallet_unit_id, issued_at DESC);

-- +goose Down
DROP TABLE wallet_unit_attestations;
