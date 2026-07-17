-- +goose Up
-- One PostGuard for Business API key per organization, AES-GCM encrypted at rest.
-- key_last4 is kept in clear for display only; the key material lives only in
-- encrypted_key (nonce || ciphertext) and is never logged or audited.
CREATE TABLE postguard_api_keys
(
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL UNIQUE REFERENCES organizations (id) ON DELETE CASCADE,
    encrypted_key   BYTEA       NOT NULL,
    key_last4       TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE postguard_api_keys;
