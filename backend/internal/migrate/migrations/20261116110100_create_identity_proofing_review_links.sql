-- +goose Up
-- A review link opens the review of one session on the wallet's hosted
-- review page, for a reviewer of the org outside the wallet. The org's API key
-- makes it for the reviewer it names; it lasts an hour, and stops with its key.
-- Only the SHA-256 of its token is kept.
CREATE TABLE identity_proofing_review_links
(
    id              UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    request_id      UUID        NOT NULL REFERENCES identity_proofing_requests (id) ON DELETE CASCADE,
    api_key_id      UUID        NOT NULL REFERENCES identity_proofing_api_keys (id) ON DELETE CASCADE,
    token_hash      BYTEA       NOT NULL UNIQUE,
    reviewer        TEXT        NOT NULL CHECK (btrim(reviewer) <> ''),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL
);

CREATE INDEX identity_proofing_review_links_request_idx ON identity_proofing_review_links (request_id);

-- +goose Down
DROP TABLE identity_proofing_review_links;
