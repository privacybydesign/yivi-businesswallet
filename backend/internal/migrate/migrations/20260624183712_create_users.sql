-- +goose Up
CREATE TABLE users
(
    id             UUID PRIMARY KEY     DEFAULT uuidv7(),
    email          TEXT        NOT NULL UNIQUE,
    preferred_name TEXT,
    given_names    TEXT        NOT NULL,
    name_prefix    TEXT,
    last_name      TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE users;
