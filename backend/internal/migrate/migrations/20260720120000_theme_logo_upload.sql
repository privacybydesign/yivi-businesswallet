-- +goose Up
-- Replace the theme logo URI (a path or a pasted data: URI) with an uploaded
-- image stored as bytes, so an admin can upload a logo file directly instead of
-- hosting it elsewhere or hand-encoding it. logo_bytes holds the raw image
-- (NULL when unset); logo_content_type is its MIME type (e.g. "image/png"). The
-- API serves the bytes at /theme/logo and returns that path as the theme logo.
ALTER TABLE org_theme_settings
    DROP COLUMN logo_uri,
    ADD COLUMN logo_bytes        BYTEA,
    ADD COLUMN logo_content_type TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE org_theme_settings
    DROP COLUMN logo_bytes,
    DROP COLUMN logo_content_type,
    ADD COLUMN logo_uri TEXT NOT NULL DEFAULT '';
