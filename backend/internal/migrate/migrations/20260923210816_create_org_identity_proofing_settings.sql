-- +goose Up
-- org_identity_proofing_settings links an org to its tenant at the
-- identity-proofing-service (IPS). One org is one IPS tenant: the org's first use
-- of identity proofing makes the wallet create the tenant and a scoped API key
-- through the IPS admin API, and this row is the result. No row means the org
-- has not used proofing yet.
--
-- api_key_ciphertext / webhook_secret_ciphertext are sealed with the deployment
-- IDENTITY_PROOFING_ENCRYPTION_KEY (internal/crypto): IPS shows both exactly
-- once, at creation, so they are kept rather than re-minted. The webhook secret
-- is stored for the later webhook integration; nothing reads it yet.
CREATE TABLE org_identity_proofing_settings
(
    organization_id           UUID PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    ips_tenant_id             TEXT        NOT NULL,
    api_key_ciphertext        BYTEA       NOT NULL,
    webhook_secret_ciphertext BYTEA,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE org_identity_proofing_settings;
