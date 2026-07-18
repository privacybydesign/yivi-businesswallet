-- +goose Up
-- Per-organization theming so the business wallet reflects the tenant's identity
-- rather than a generic Yivi look. primary_color re-colours the primary action
-- token (buttons, active nav); accent_color re-colours the accent/brand token;
-- logo_uri is the org logo shown in place of the default wordmark. Colours are
-- stored as CSS hex strings (e.g. "#1d4e89"); the logo is a URI (http(s) or a
-- data: URI), mirroring org_issuer_settings.logo_uri — binary/object storage is
-- out of scope here. Empty string means "unset": fall back to the default look.
CREATE TABLE org_theme_settings
(
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL UNIQUE REFERENCES organizations (id) ON DELETE CASCADE,
    primary_color   TEXT        NOT NULL DEFAULT '',
    accent_color    TEXT        NOT NULL DEFAULT '',
    logo_uri        TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE org_theme_settings;
