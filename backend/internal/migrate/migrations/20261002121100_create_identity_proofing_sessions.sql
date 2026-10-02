-- +goose Up
-- A proofing session the wallet's engine runs: the Idem app claims it with a
-- grant token and submits its steps to /api/v1/app/{token}/... The columns
-- are what lookups and sweeps need; everything else, the evidence included,
-- is in data, sealed under IDENTITY_PROOFING_ENCRYPTION_KEY. Only hashes of
-- the session token and of each slot's pending grant are kept.
CREATE TABLE identity_proofing_sessions
(
    id                         TEXT PRIMARY KEY,
    organization_id            UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    token_hash                 TEXT        NOT NULL UNIQUE,
    status                     TEXT        NOT NULL,
    web_grant_hash             TEXT        NOT NULL DEFAULT '',
    native_grant_hash          TEXT        NOT NULL DEFAULT '',
    expires_at                 TIMESTAMPTZ NOT NULL,
    completed_at               TIMESTAMPTZ,
    retention_override_seconds INTEGER     NOT NULL DEFAULT 0,
    data                       BYTEA       NOT NULL,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX identity_proofing_sessions_org_idx
    ON identity_proofing_sessions (organization_id);
CREATE INDEX identity_proofing_sessions_web_grant_idx
    ON identity_proofing_sessions (web_grant_hash) WHERE web_grant_hash <> '';
CREATE INDEX identity_proofing_sessions_native_grant_idx
    ON identity_proofing_sessions (native_grant_hash) WHERE native_grant_hash <> '';
CREATE INDEX identity_proofing_sessions_open_idx
    ON identity_proofing_sessions (expires_at)
    WHERE status NOT IN ('approved', 'rejected', 'expired', 'cancelled', 'needs_review');

-- +goose Down
DROP TABLE identity_proofing_sessions;
