-- +goose Up
-- A data-portability export assembled in the background (Art 5(1)(l)). The
-- synchronous endpoint stays for API callers; an organisation whose evidence and
-- attachments outgrow one request builds its bundle here instead.
--
-- The finished bundle is stored inline, like every other payload this codebase
-- keeps (qerds attachments, theme logos, member avatars). Disk would not survive
-- a restart between assembly and download, and would strand a bundle on the
-- replica that built it.
--
-- The download token is stored hashed and never in the clear: it is the sole
-- credential on the unauthenticated download route, which exists because a
-- termination export has to reach an owner who can no longer sign in.
CREATE TABLE export_jobs
(
    id                  UUID        NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    organization_id     UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    status              TEXT        NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'ready', 'failed')),
    sections            TEXT[]      NOT NULL DEFAULT '{}',
    -- 'request' is an admin asking; 'termination' is the provider's own Art 7(6)(f)
    -- obligation firing, which is what decides whether the finished bundle is
    -- mailed to the organisation's admins rather than waiting to be collected.
    origin              TEXT        NOT NULL DEFAULT 'request'
        CHECK (origin IN ('request', 'termination')),
    -- The trail survives the person: an export stays attributable after the
    -- account that asked for it is gone.
    requested_by        UUID                 REFERENCES users (id) ON DELETE SET NULL,
    bundle_id           UUID,
    filename            TEXT        NOT NULL DEFAULT '',
    content             BYTEA,
    size_bytes          BIGINT      NOT NULL DEFAULT 0,
    checksum            TEXT        NOT NULL DEFAULT '',
    download_token_hash TEXT,
    downloaded_at       TIMESTAMPTZ,
    error               TEXT        NOT NULL DEFAULT '',
    expires_at          TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The org's own history, newest first.
CREATE INDEX idx_export_jobs_org ON export_jobs (organization_id, created_at DESC);
-- The worker's claim: the oldest queued job, whatever organisation it belongs to.
CREATE INDEX idx_export_jobs_queued ON export_jobs (created_at) WHERE status = 'queued';
-- Resolves a download token to its job in one lookup.
CREATE UNIQUE INDEX idx_export_jobs_token ON export_jobs (download_token_hash)
    WHERE download_token_hash IS NOT NULL;

-- +goose Down
DROP TABLE export_jobs;
