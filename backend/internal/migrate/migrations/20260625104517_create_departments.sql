-- +goose Up
CREATE TABLE departments
(
    id              UUID PRIMARY KEY     DEFAULT uuidv7(),
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name            TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name),
    UNIQUE (id, organization_id)
);

CREATE INDEX idx_departments_organization_id ON departments (organization_id);

-- +goose Down
DROP TABLE departments;
