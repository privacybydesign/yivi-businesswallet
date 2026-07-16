-- +goose Up
-- Per-organization SMTP configuration for outbound e-mail (e.g. "your credential
-- is ready" notifications to natural-person recipients). The password is stored
-- encrypted at rest (AES-256-GCM under the deployment e-mail encryption key); the
-- row never holds the plaintext password.
CREATE TABLE org_email_settings
(
    id                   UUID        PRIMARY KEY DEFAULT uuidv7(),
    organization_id      UUID        NOT NULL UNIQUE REFERENCES organizations (id) ON DELETE CASCADE,
    host                 TEXT        NOT NULL,
    port                 INTEGER     NOT NULL,
    username             TEXT        NOT NULL DEFAULT '',
    password_ciphertext  BYTEA,
    from_name            TEXT        NOT NULL DEFAULT '',
    from_address         TEXT        NOT NULL,
    enabled              BOOLEAN     NOT NULL DEFAULT true,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE org_email_settings;
