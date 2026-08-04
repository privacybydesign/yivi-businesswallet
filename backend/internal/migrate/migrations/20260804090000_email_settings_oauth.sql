-- +goose Up
-- Microsoft 365 (Exchange Online) submission. Microsoft has turned off Basic
-- Authentication for SMTP AUTH on most tenants, so a password is no longer a
-- usable credential there: modern client submission authenticates with an OAuth2
-- bearer token over SASL XOAUTH2. auth_mechanism picks which of the two a send
-- uses; the app registration's client secret is stored encrypted at rest under
-- the same deployment e-mail key as the SMTP password.
ALTER TABLE org_email_settings
    ADD COLUMN auth_mechanism           TEXT NOT NULL DEFAULT 'plain'
        CONSTRAINT org_email_settings_auth_mechanism_check CHECK (auth_mechanism IN ('plain', 'xoauth2')),
    ADD COLUMN tenant_id                TEXT NOT NULL DEFAULT '',
    ADD COLUMN client_id                TEXT NOT NULL DEFAULT '',
    ADD COLUMN client_secret_ciphertext BYTEA;

-- +goose Down
ALTER TABLE org_email_settings
    DROP COLUMN auth_mechanism,
    DROP COLUMN tenant_id,
    DROP COLUMN client_id,
    DROP COLUMN client_secret_ciphertext;
