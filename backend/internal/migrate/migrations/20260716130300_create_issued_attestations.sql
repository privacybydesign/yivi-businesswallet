-- +goose Up
-- The issuance ledger (the "Issued" tab) — the transaction log the regulation
-- requires (Art 5(1)(m)). One row per issuance; never hard-deleted (revoke is a
-- state change). schema_vct and qualified are snapshotted at issue-time so the
-- ledger stays truthful if the schema/template later change. issuance_id is the
-- opaque correlation key into the hosted issuer (openid4vciissuer).
CREATE TABLE issued_attestations
(
    id                    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id       UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    template_id           UUID        REFERENCES attestation_templates (id) ON DELETE SET NULL,
    schema_vct            TEXT        NOT NULL,
    recipient_kind        TEXT        NOT NULL CHECK (recipient_kind IN ('member', 'external', 'organization')),
    recipient_user_id     UUID        REFERENCES users (id) ON DELETE SET NULL,
    recipient_ref         TEXT        NOT NULL,
    attributes            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    qualified             BOOLEAN     NOT NULL DEFAULT false,
    status                TEXT        NOT NULL CHECK (status IN ('offered', 'claimed', 'expired', 'revoked', 'failed')),
    issuance_id           TEXT        NOT NULL DEFAULT '',
    -- The created credential offer, persisted so the recipient's claim page can
    -- render it after delivery (e-mail / QERDS). offer_uri carries the pre-auth
    -- code; claim_token is the opaque, unguessable key the public claim page uses
    -- (never the row id, which appears in admin APIs).
    offer_uri             TEXT        NOT NULL DEFAULT '',
    tx_code               TEXT        NOT NULL DEFAULT '',
    claim_token           TEXT,
    delivery              TEXT        NOT NULL DEFAULT 'none' CHECK (delivery IN ('none', 'email', 'qerds')),
    linked_attestation_id UUID        REFERENCES issued_attestations (id) ON DELETE SET NULL,
    qualified_timestamp   TIMESTAMPTZ,
    issued_by_user_id     UUID        REFERENCES users (id) ON DELETE SET NULL,
    claimed_at            TIMESTAMPTZ,
    expires_at            TIMESTAMPTZ,
    revoked_at            TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_issued_attestations_organization_id ON issued_attestations (organization_id);
CREATE INDEX idx_issued_attestations_template_id ON issued_attestations (template_id);
CREATE UNIQUE INDEX idx_issued_attestations_claim_token ON issued_attestations (claim_token);

-- +goose Down
DROP TABLE issued_attestations;
