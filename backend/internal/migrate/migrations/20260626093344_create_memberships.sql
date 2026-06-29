-- +goose Up
CREATE TABLE memberships
(
    user_id         UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    role            TEXT        NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
    job_title       TEXT,
    department_id   UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, organization_id),
    -- NO ACTION (not RESTRICT) defers to end-of-statement so an org delete cascades
    -- departments and memberships together without colliding.
    -- Named so AddMembership can tell this apart from the other 23503 FK violations.
    CONSTRAINT memberships_department_fkey FOREIGN KEY (department_id, organization_id) REFERENCES departments (id, organization_id)
);

CREATE INDEX idx_memberships_organization_id ON memberships (organization_id);

-- +goose Down
DROP TABLE memberships;
