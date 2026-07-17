-- +goose Up
CREATE TABLE identity_reviews
(
    id                    UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    user_id               UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    invitation_id         UUID        NOT NULL UNIQUE REFERENCES invitations (id) ON DELETE CASCADE,
    stored_given_names    TEXT        NOT NULL,
    stored_last_name      TEXT        NOT NULL,
    disclosed_given_names TEXT        NOT NULL,
    disclosed_last_name   TEXT        NOT NULL,
    status                TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    reviewed_by           UUID REFERENCES users (id) ON DELETE SET NULL,
    reviewed_at           TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_identity_reviews_status ON identity_reviews (status);

-- +goose Down
DROP TABLE identity_reviews;
