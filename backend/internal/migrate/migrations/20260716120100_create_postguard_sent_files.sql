-- +goose Up
-- History of encrypted file transfers sent via PostGuard. cryptify_uuid is the
-- identifier the hosted PostGuard storage (cryptify) returns; recipients are the
-- e-mail identities the payload was encrypted to. No file content is stored here.
CREATE TABLE postguard_sent_files
(
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    sender_user_id  UUID        REFERENCES users (id) ON DELETE SET NULL,
    file_name       TEXT        NOT NULL,
    size_bytes      BIGINT      NOT NULL,
    recipients      TEXT[]      NOT NULL,
    cryptify_uuid   TEXT        NOT NULL,
    expires_after   TEXT,
    status          TEXT        NOT NULL DEFAULT 'sent',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT postguard_sent_files_status_check CHECK (status IN ('sent'))
);

CREATE INDEX idx_postguard_sent_files_organization ON postguard_sent_files (organization_id, created_at DESC);

-- +goose Down
DROP TABLE postguard_sent_files;
