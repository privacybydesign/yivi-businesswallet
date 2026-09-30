-- +goose Up
-- Test mode: each org gets a second, sandbox IPS tenant on first test use,
-- customer API keys are live or test, and a request records which it ran in.
-- Existing keys and requests are live.
ALTER TABLE org_identity_proofing_settings
    ADD COLUMN sandbox_tenant_id TEXT,
    ADD COLUMN sandbox_api_key_ciphertext BYTEA,
    ADD COLUMN sandbox_webhook_secret_ciphertext BYTEA;
CREATE UNIQUE INDEX org_identity_proofing_settings_sandbox_tenant_idx
    ON org_identity_proofing_settings (sandbox_tenant_id);
ALTER TABLE identity_proofing_api_keys
    ADD COLUMN mode TEXT NOT NULL DEFAULT 'live' CHECK (mode IN ('live', 'test'));
ALTER TABLE identity_proofing_requests
    ADD COLUMN mode TEXT NOT NULL DEFAULT 'live' CHECK (mode IN ('live', 'test'));

-- +goose Down
ALTER TABLE identity_proofing_requests DROP COLUMN mode;
ALTER TABLE identity_proofing_api_keys DROP COLUMN mode;
DROP INDEX org_identity_proofing_settings_sandbox_tenant_idx;
ALTER TABLE org_identity_proofing_settings
    DROP COLUMN sandbox_webhook_secret_ciphertext,
    DROP COLUMN sandbox_api_key_ciphertext,
    DROP COLUMN sandbox_tenant_id;
