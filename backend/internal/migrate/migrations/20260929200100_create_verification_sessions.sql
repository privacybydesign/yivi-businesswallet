-- +goose Up
-- One row per verification an organisation runs from a template (issue #245):
-- the hosted verifier's transaction_id stays server-side (the browser polls by
-- our own id), the wallet link is kept so a reloaded page can redraw the QR,
-- and the disclosed claims plus the check outcomes land here on completion so
-- the row doubles as the check history. template_id is SET NULL on delete;
-- template_name keeps the history readable afterwards.
CREATE TABLE verification_sessions
(
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id    UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    template_id        UUID        REFERENCES verification_templates (id) ON DELETE SET NULL,
    template_name      TEXT        NOT NULL,
    vct                TEXT        NOT NULL,
    transaction_id     TEXT        NOT NULL,
    wallet_link        TEXT        NOT NULL,
    status             TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed')),
    started_by_user_id UUID        REFERENCES users (id) ON DELETE SET NULL,
    claims             JSONB,
    checks             JSONB,
    valid              BOOLEAN,
    completed_at       TIMESTAMPTZ,
    expires_at         TIMESTAMPTZ NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_verification_sessions_organization_id ON verification_sessions (organization_id, created_at DESC);

-- +goose Down
DROP TABLE verification_sessions;
