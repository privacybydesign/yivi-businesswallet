-- +goose Up
-- Per-user avatar photo, stored as bytes like the org theme logo so a deployment
-- needs no object store. The image lives in its own table rather than a column on
-- `users`: that row is read on every authenticated request (session lookup), and
-- the payload should only be fetched when the image is actually served. One row
-- per user, no row means no avatar; updated_at doubles as the cache-busting
-- version in the URL the API hands out.
CREATE TABLE user_avatars (
    user_id      UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    bytes        BYTEA       NOT NULL,
    content_type TEXT        NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE user_avatars;
