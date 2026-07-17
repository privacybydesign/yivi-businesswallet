-- +goose Up
-- Per-organization data-encryption key (DEK) for PostGuard, configurable by the
-- business wallet owner. Stored wrapped (envelope encryption): wrapped_dek is the
-- org's DEK sealed with the deployment master key (POSTGUARD_KEY_ENCRYPTION_KEY),
-- so a database dump alone cannot decrypt it. The DEK in turn encrypts the org's
-- PostGuard API key in postguard_api_keys.
CREATE TABLE postguard_org_keys
(
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL UNIQUE REFERENCES organizations (id) ON DELETE CASCADE,
    wrapped_dek     BYTEA       NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE postguard_org_keys;
