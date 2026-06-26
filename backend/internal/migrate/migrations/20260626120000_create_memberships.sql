-- +goose Up
CREATE TABLE memberships
(
    user_id         UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    role            TEXT        NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, organization_id)
);

CREATE INDEX idx_memberships_organization_id ON memberships (organization_id);

-- +goose Down
DROP TABLE memberships;
