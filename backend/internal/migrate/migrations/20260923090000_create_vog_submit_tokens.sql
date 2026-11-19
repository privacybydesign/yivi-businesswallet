-- +goose Up
-- vog_submit_tokens is the entry point for a member's VOG submission (#242): a
-- bearer token, hashed like an invite or re-identification token, that opens
-- the public /vog/<token> page, so a member can submit a VOG from the e-mail
-- without signing in - the same posture as a credential claim link. One live
-- token per membership: an admin's "request VOG", the scheduler's reminder
-- e-mail and the member's own dashboard banner all mint or rotate the same row
-- (EnsureVogToken). A valid screening retires it.
CREATE TABLE vog_submit_tokens
(
    id               UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    user_id          UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    organization_id  UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    token_hash       BYTEA       NOT NULL UNIQUE,
    expires_at       TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, organization_id),
    CONSTRAINT vog_submit_tokens_membership_fkey FOREIGN KEY (user_id, organization_id) REFERENCES memberships (user_id, organization_id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE vog_submit_tokens;
