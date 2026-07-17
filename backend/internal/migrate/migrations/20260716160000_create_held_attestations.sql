-- +goose Up
-- Thin org-scoped index over the credentials the organization HOLDS (Art 5(1)(a)
-- "store, select, combine"). The credential material itself lives in the irmago
-- EUDI holder engine (see .ai/features/attestations.md §6.5); this table points at
-- the irmago-owned credential via credential_ref and does not duplicate the claims.
-- It exists for org-scoping, listing, audit and the QERDS evidence link.
CREATE TABLE held_attestations
(
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    -- id of the irmago IssuedCredentialInstance this row indexes (opaque to us).
    credential_ref    TEXT        NOT NULL,
    vct               TEXT        NOT NULL,
    issuer            TEXT        NOT NULL,
    -- how the held credential arrived.
    source            TEXT        NOT NULL CHECK (source IN ('qerds', 'openid4vci', 'bootstrap')),
    -- evidence chain when it was delivered over QERDS; NULL otherwise.
    source_message_id UUID        REFERENCES qerds_messages (id) ON DELETE SET NULL,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- soft-delete keeps the trail (Art 5(1)(m)); active rows have deleted_at IS NULL.
    deleted_at        TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_held_attestations_org ON held_attestations (organization_id, received_at DESC)
    WHERE deleted_at IS NULL;

-- +goose Down
DROP TABLE held_attestations;
