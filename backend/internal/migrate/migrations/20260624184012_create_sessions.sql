-- +goose Up
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
