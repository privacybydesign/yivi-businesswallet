-- +goose Up
-- A user's linked signing credential at a per-org CSC signing provider (a remote
-- QTSP). Cached once at link time so the signing ceremony is a single OAuth
-- authorize: the CMS SignedAttributes hash the signing certificate, so the cert
-- must be known before the document hash is computed and bound into the authorize
-- step. No key material is ever held — the QTSP owns the key; this row holds only
-- the public certificate chain (PEM) and the QTSP credential id + key algorithm.
CREATE TABLE signing_credentials
(
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id         UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    credential_id   TEXT        NOT NULL,
    certificate_pem TEXT        NOT NULL,
    chain_pem       TEXT        NOT NULL,
    key_algo        TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, user_id)
);

-- +goose Down
DROP TABLE signing_credentials;
