-- +goose Up
-- An org's IPS tenant is now created under the org's own id, so the wallet keeps
-- no tenant id of its own; test requests run on the same tenant with a test key
-- instead of on a second sandbox tenant. Existing rows point at IPS tenants with
-- generated ids, so they are dropped: each org provisions afresh on next use
-- (with its flow selection), and its old requests settle as expired.
DELETE FROM org_identity_proofing_settings;
DROP INDEX org_identity_proofing_settings_ips_tenant_idx;
DROP INDEX org_identity_proofing_settings_sandbox_tenant_idx;
ALTER TABLE org_identity_proofing_settings
    DROP COLUMN ips_tenant_id,
    DROP COLUMN sandbox_tenant_id,
    DROP COLUMN sandbox_webhook_secret_ciphertext;
ALTER TABLE org_identity_proofing_settings
    RENAME COLUMN sandbox_api_key_ciphertext TO test_api_key_ciphertext;
ALTER TABLE org_identity_proofing_settings
    ALTER COLUMN test_api_key_ciphertext SET NOT NULL,
    ALTER COLUMN webhook_secret_ciphertext SET NOT NULL;

-- +goose Down
DELETE FROM org_identity_proofing_settings;
ALTER TABLE org_identity_proofing_settings
    ALTER COLUMN webhook_secret_ciphertext DROP NOT NULL,
    ALTER COLUMN test_api_key_ciphertext DROP NOT NULL;
ALTER TABLE org_identity_proofing_settings
    RENAME COLUMN test_api_key_ciphertext TO sandbox_api_key_ciphertext;
ALTER TABLE org_identity_proofing_settings
    ADD COLUMN ips_tenant_id TEXT NOT NULL,
    ADD COLUMN sandbox_tenant_id TEXT,
    ADD COLUMN sandbox_webhook_secret_ciphertext BYTEA;
CREATE UNIQUE INDEX org_identity_proofing_settings_sandbox_tenant_idx
    ON org_identity_proofing_settings (sandbox_tenant_id);
CREATE UNIQUE INDEX org_identity_proofing_settings_ips_tenant_idx
    ON org_identity_proofing_settings (ips_tenant_id);
