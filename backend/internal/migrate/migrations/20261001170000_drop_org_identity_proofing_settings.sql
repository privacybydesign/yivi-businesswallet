-- +goose Up
-- The wallet runs identity proofing itself now: there is no
-- identity-proofing-service tenant to link an org to, and no IPS API keys or
-- webhook secret to keep. The flow allow-list hung off this table; it now
-- hangs off the organization directly.
ALTER TABLE org_identity_proofing_flows
    DROP CONSTRAINT org_identity_proofing_flows_organization_id_fkey,
    ADD CONSTRAINT org_identity_proofing_flows_organization_id_fkey
        FOREIGN KEY (organization_id) REFERENCES organizations (id) ON DELETE CASCADE;
DROP TABLE org_identity_proofing_settings;

-- +goose Down
CREATE TABLE org_identity_proofing_settings
(
    organization_id           UUID PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    api_key_ciphertext        BYTEA       NOT NULL,
    test_api_key_ciphertext   BYTEA       NOT NULL,
    webhook_secret_ciphertext BYTEA       NOT NULL,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- No org has IPS keys again, so no selection can point at a settings row.
DELETE FROM org_identity_proofing_flows;
ALTER TABLE org_identity_proofing_flows
    DROP CONSTRAINT org_identity_proofing_flows_organization_id_fkey,
    ADD CONSTRAINT org_identity_proofing_flows_organization_id_fkey
        FOREIGN KEY (organization_id) REFERENCES org_identity_proofing_settings (organization_id) ON DELETE CASCADE;
