-- +goose Up
-- A self-managed portrait photo per user, shown as their avatar wherever they are
-- represented (member lists, audit log). The image is stored as bytes on the user
-- row because identity is central to a deployment: the avatar follows the person
-- across every organisation they are a member of, not per membership.
-- avatar_bytes holds the normalised image (NULL when unset), avatar_content_type
-- its MIME type, and avatar_updated_at is the cache-busting version the API puts
-- in the URL that serves it. The API re-encodes every upload to a fixed square
-- JPEG, so no camera metadata (EXIF, GPS) reaches these columns.
ALTER TABLE users
    ADD COLUMN avatar_bytes        BYTEA,
    ADD COLUMN avatar_content_type TEXT NOT NULL DEFAULT '',
    ADD COLUMN avatar_updated_at   TIMESTAMPTZ;

-- +goose Down
ALTER TABLE users
    DROP COLUMN avatar_bytes,
    DROP COLUMN avatar_content_type,
    DROP COLUMN avatar_updated_at;
