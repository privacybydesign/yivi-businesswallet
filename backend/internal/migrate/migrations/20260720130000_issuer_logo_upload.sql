-- +goose Up
-- Replace the issuer logo URI (a pasted URL or data: URI) with an uploaded image
-- stored as bytes, so an admin can upload a logo file directly instead of hosting
-- it elsewhere or hand-encoding a data URI. logo_bytes holds the raw image (NULL
-- when unset); logo_content_type is its MIME type (e.g. "image/png"). The API
-- serves the bytes at /issuer/settings/logo for the admin preview, and the
-- generated issuer bundle embeds them as a self-contained data: URI (the hosted
-- issuer's wallet-facing metadata cannot reach a business-wallet endpoint).
ALTER TABLE org_issuer_settings
    DROP COLUMN logo_uri,
    ADD COLUMN logo_bytes        BYTEA,
    ADD COLUMN logo_content_type TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE org_issuer_settings
    DROP COLUMN logo_bytes,
    DROP COLUMN logo_content_type,
    ADD COLUMN logo_uri TEXT NOT NULL DEFAULT '';
