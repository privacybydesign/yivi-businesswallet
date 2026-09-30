-- +goose Up
-- IPS pushes session changes signed per tenant: the wallet finds the org by its
-- tenant and the request by its session.
CREATE UNIQUE INDEX org_identity_proofing_settings_ips_tenant_idx
    ON org_identity_proofing_settings (ips_tenant_id);
CREATE INDEX identity_proofing_requests_ips_session_idx
    ON identity_proofing_requests (ips_session_id);

-- +goose Down
DROP INDEX identity_proofing_requests_ips_session_idx;
DROP INDEX org_identity_proofing_settings_ips_tenant_idx;
