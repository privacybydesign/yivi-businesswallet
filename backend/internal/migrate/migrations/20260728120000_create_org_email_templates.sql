-- +goose Up
-- Per-organization overrides of the shipped transactional mail copy: one row per
-- (organization, kind, locale). A missing row means the org has not customised
-- that cause in that language, so internal/email falls back to the embedded
-- default (templates/defaults.<locale>.json). The table therefore only ever holds
-- tenant edits — never a copy of the defaults — so improving the shipped wording
-- still reaches every org that has not overridden it.
--
-- The columns mirror email.Template: prose plus a call to action, never HTML. The
-- mail-client-safe layout is internal/email/shell.go's alone, so a tenant cannot
-- author markup and there is no stored markup to sanitise. Placeholders are
-- {{variable}} references validated against the kind's allowlist before a row is
-- written (email.ValidateTemplate), so an unknown placeholder is a save-time
-- error rather than a blank in delivered mail.
CREATE TABLE org_email_templates
(
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    kind            TEXT        NOT NULL,
    locale          TEXT        NOT NULL,
    subject         TEXT        NOT NULL,
    preheader       TEXT        NOT NULL DEFAULT '',
    headline        TEXT        NOT NULL,
    paragraphs      TEXT[]      NOT NULL DEFAULT '{}',
    cta_label       TEXT        NOT NULL DEFAULT '',
    cta_url         TEXT        NOT NULL DEFAULT '',
    link_fallback   TEXT        NOT NULL DEFAULT '',
    note            TEXT        NOT NULL DEFAULT '',
    footer          TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, kind, locale)
);

-- +goose Down
DROP TABLE org_email_templates;
