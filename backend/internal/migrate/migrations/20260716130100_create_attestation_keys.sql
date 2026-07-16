-- +goose Up
-- Signing key material references. We never store private keys: provider_ref
-- points at key material held in the hosted issuer / secure cryptographic device
-- (Art 6(1)(l)); certificate_pem is the public chain for a qualified certificate
-- (Art 5(1)(d)/(h)). kind selects qualified vs non-qualified issuance — the same
-- code path, different seal.
CREATE TABLE attestation_keys
(
    id              UUID        PRIMARY KEY DEFAULT uuidv7(),
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    kind            TEXT        NOT NULL CHECK (kind IN ('wallet_managed', 'qualified_certificate')),
    label           TEXT        NOT NULL,
    provider_ref    TEXT        NOT NULL DEFAULT '',
    certificate_pem TEXT,
    status          TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'revoked')),
    valid_from      TIMESTAMPTZ,
    valid_until     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_attestation_keys_organization_id ON attestation_keys (organization_id);

-- +goose Down
DROP TABLE attestation_keys;
