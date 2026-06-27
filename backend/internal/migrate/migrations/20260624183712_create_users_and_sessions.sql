-- +goose Up
CREATE TABLE users
(
    id             UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    email          TEXT        NOT NULL UNIQUE,
    preferred_name TEXT,
    given_names    TEXT        NOT NULL,
    name_prefix    TEXT,
    last_name      TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions
(
    token_hash      BYTEA PRIMARY KEY,
    user_id         UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    idempotency_key BYTEA UNIQUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_sessions_user_id ON sessions (user_id);

-- +goose Down
DROP TABLE sessions;
DROP TABLE users;
