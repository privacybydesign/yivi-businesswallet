-- +goose Up
-- identity_reverify_tokens is the entry point for a member's re-identification
-- (#240 §5): a bearer token, hashed like an invite token, that starts the same
-- identity-disclosure flow invite-accept uses. One live token per membership —
-- an admin's "request identification", the scheduler's reminder email and the
-- member's own in-app banner all resolve to the same row, minted or rotated on
-- demand (EnsureReverifyToken), so there is exactly one mechanism regardless of
-- entry point.
CREATE TABLE identity_reverify_tokens
(
    id               UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    user_id          UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    organization_id  UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    token_hash       BYTEA       NOT NULL UNIQUE,
    expires_at       TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, organization_id),
    CONSTRAINT identity_reverify_tokens_membership_fkey FOREIGN KEY (user_id, organization_id) REFERENCES memberships (user_id, organization_id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE identity_reverify_tokens;
