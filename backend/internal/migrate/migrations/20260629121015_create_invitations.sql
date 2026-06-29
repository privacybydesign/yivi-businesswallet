-- +goose Up
CREATE TABLE invitations
(
    id                  UUID PRIMARY KEY     DEFAULT uuidv7(),
    organization_id     UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    email               TEXT        NOT NULL,
    invited_by          UUID REFERENCES users (id) ON DELETE SET NULL,
    role                TEXT        NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
    job_title           TEXT,
    department_id       UUID,
    invited_given_names TEXT        NOT NULL,
    invited_last_name   TEXT        NOT NULL,
    invite_token_hash   BYTEA       NOT NULL UNIQUE,
    expires_at          TIMESTAMPTZ NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (email, organization_id),
    CONSTRAINT invitations_department_fkey FOREIGN KEY (department_id, organization_id) REFERENCES departments (id, organization_id)
);

CREATE INDEX idx_invitations_organization_id ON invitations (organization_id);
CREATE INDEX idx_invitations_email ON invitations (email);

-- +goose Down
DROP TABLE invitations;
