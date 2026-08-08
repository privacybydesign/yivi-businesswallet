-- +goose Up
-- Per-organization connection settings for a Cloud Signature Consortium (CSC)
-- API v2 signing provider (a remote QTSP). The business wallet is the requestor
-- that drives the QTSP's CSC API; this row holds how to reach one.
--
-- provider_kind is validated in Go (internal/csc), not by a DB CHECK, so adding a
-- new kind is a Go-only change: `sample` is the self-hosted reference QTSP
-- (docker compose --profile signer), `custom` is any other CSC v2 endpoint.
--
-- client_id is the (non-secret) OAuth client id registered at the QTSP; the
-- client secret is the credential and is stored encrypted at rest (AES-256-GCM
-- under CSC_ENCRYPTION_KEY) — the row never holds the plaintext secret, and it is
-- only needed for the authenticated signing ceremony, not the connection test.
-- `enabled` turns the configured provider on without discarding it.
CREATE TABLE org_csc_settings
(
    organization_id          UUID        PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    provider_kind            TEXT        NOT NULL DEFAULT 'sample',
    base_url                 TEXT        NOT NULL DEFAULT '',
    client_id                TEXT        NOT NULL DEFAULT '',
    client_secret_ciphertext BYTEA,
    enabled                  BOOLEAN     NOT NULL DEFAULT false,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE org_csc_settings;
