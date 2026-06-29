-- +goose Up
-- SET NULL (not cascade): deleting a user/org must never erase the audit trail.
CREATE TABLE audit_events
(
    id              UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_user_id   UUID REFERENCES users (id) ON DELETE SET NULL,
    organization_id UUID REFERENCES organizations (id) ON DELETE SET NULL,
    action          TEXT        NOT NULL,
    target_type     TEXT        NOT NULL,
    target_id       TEXT        NOT NULL,
    metadata        JSONB       NOT NULL DEFAULT '{}',
    request_id      TEXT
);

CREATE INDEX idx_audit_events_organization_id_occurred_at ON audit_events (organization_id, occurred_at);
CREATE INDEX idx_audit_events_actor_user_id_occurred_at ON audit_events (actor_user_id, occurred_at);

-- +goose Down
DROP TABLE audit_events;
