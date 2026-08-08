-- +goose Up
-- One qualified-signing request: a PDF a user asked to have signed by the org's
-- CSC signing provider. The request is created when the signing ceremony starts
-- (status awaiting_authorization) and the signed PAdES document is stored on it
-- once the ceremony completes. `error` carries a redaction-safe reason on failure.
CREATE TABLE signing_requests
(
    id              UUID        PRIMARY KEY,
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id         UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    status          TEXT        NOT NULL,
    credential_id   TEXT        NOT NULL,
    filename        TEXT        NOT NULL DEFAULT '',
    signed_document BYTEA,
    error           TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_signing_requests_org_user ON signing_requests (organization_id, user_id);

-- +goose Down
DROP TABLE signing_requests;
