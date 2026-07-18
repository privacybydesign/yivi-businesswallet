-- +goose Up
-- Per-organization WSCA (Wallet Secure Cryptographic Application) account state
-- for the business wallet's holder-binding keys. A self-managing business wallet
-- signs headlessly, so the SECDSA activation secret (the knowledge factor the
-- WSCA needs on every sign) is stored encrypted at rest (AES-256-GCM under the
-- deployment WSCA key-encryption key) and decrypted only in-memory at sign time;
-- the row never holds the plaintext secret. account_id is the WSCA account
-- (hex(sha256(DER(U)))), stable across secret rotation. See
-- .ai/features/wsca-holder-binding.md.
CREATE TABLE org_wsca_accounts
(
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id    UUID        NOT NULL UNIQUE REFERENCES organizations (id) ON DELETE CASCADE,
    account_id         TEXT        NOT NULL,
    certificate_id     TEXT        NOT NULL DEFAULT '',
    secret_ciphertext  BYTEA       NOT NULL,
    activated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    rotated_at         TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE org_wsca_accounts;
