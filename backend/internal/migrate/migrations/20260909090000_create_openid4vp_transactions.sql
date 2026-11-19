-- +goose Up
-- openid4vp_transactions is the state machine behind an inbound OpenID4VP
-- Authorization Request: an external verifier invokes the business wallet at
-- GET /openid4vp?client_id=…&request_uri=…, the backend fetches and validates
-- the Request Object and keeps it here under a server-minted opaque id. Only the
-- id's hash is stored (mirrors presentation_sessions), so a DB read cannot
-- reveal a live bearer; the raw inbound parameters and the validated request
-- never travel back to the browser, which only ever carries the opaque id — also
-- through the login redirect. See .ai/features/openid4vp-inbound.md.
CREATE TABLE openid4vp_transactions
(
    id                 UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    id_hash            BYTEA       NOT NULL UNIQUE,
    -- The inbound parameters as received.
    client_id          TEXT        NOT NULL,
    request_uri        TEXT        NOT NULL,
    request_uri_method TEXT        NOT NULL,
    -- Resolved from the validated Request Object's client_id binding; what the
    -- org-selection UI shows as "who is asking".
    verifier_identity  TEXT        NOT NULL,
    -- From the validated Request Object. dcql_query is persisted so a slow
    -- org-picking human never requires re-fetching a possibly single-use
    -- request_uri; nonce/state/response_uri/response_mode build the eventual
    -- direct_post response.
    dcql_query         JSONB       NOT NULL,
    nonce              TEXT        NOT NULL,
    state              TEXT        NOT NULL DEFAULT '',
    response_uri       TEXT        NOT NULL,
    response_mode      TEXT        NOT NULL,
    -- The validated JAR itself (compact JWS): the response step reads the
    -- verifier's client_metadata (direct_post.jwt encryption keys) from it.
    request_object     TEXT        NOT NULL,
    -- pending_auth → org_selected → completed | denied | expired (code-defined,
    -- see internal/openid4vppresenter).
    status             TEXT        NOT NULL,
    -- Set once the browser session is authenticated, before org selection; a
    -- resumed transaction is bound to that first user.
    user_id            UUID REFERENCES users (id) ON DELETE SET NULL,
    -- Set only by org selection, always re-derived from the authenticated
    -- membership — never from a client-supplied hint.
    organization_id    UUID REFERENCES organizations (id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at         TIMESTAMPTZ NOT NULL,
    -- Set on completed/denied/expired: enforces one-time use without a second
    -- table. A select or status past this point is rejected.
    consumed_at        TIMESTAMPTZ
);

CREATE INDEX idx_openid4vp_transactions_expires_at ON openid4vp_transactions (expires_at);

-- +goose Down
DROP TABLE openid4vp_transactions;
