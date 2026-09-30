-- +goose Up
-- A hosted request is opened by its subject from a link: only the SHA-256 of
-- the link's token is kept. Its IPS session is created when the subject starts.
ALTER TABLE identity_proofing_requests
    ADD COLUMN link_token_hash BYTEA;
CREATE UNIQUE INDEX identity_proofing_requests_link_token_idx
    ON identity_proofing_requests (link_token_hash) WHERE link_token_hash IS NOT NULL;

-- +goose Down
DROP INDEX identity_proofing_requests_link_token_idx;
ALTER TABLE identity_proofing_requests
    DROP COLUMN link_token_hash;
