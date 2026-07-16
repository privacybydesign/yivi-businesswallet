-- +goose Up
-- presentation_sessions maps our opaque, client-facing session id to the
-- verifier-minted transaction_id. The transaction_id is never exposed to the
-- client: the client polls / claims with the opaque id, and the backend resolves
-- it here. Only the id's hash is stored so a DB read cannot reveal a live claim
-- bearer (mirrors the sessions table).
CREATE TABLE presentation_sessions
(
    id_hash        BYTEA PRIMARY KEY,
    transaction_id TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at     TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE presentation_sessions;
