-- +goose Up
-- notification_outbox is the transactional outbox between the audit seam and the
-- notification channels. A subscribable audit event is enqueued here in the same
-- transaction as the event itself, so a row only becomes visible once the
-- underlying action has committed — and a rolled back action enqueues nothing.
-- The dispatcher claims rows out of band and fans them out, which is what keeps a
-- failing channel from blocking or rolling back the action that caused it.
--
-- The event fields are copied rather than referencing audit_events(id): the audit
-- trail is append-only and outlives the queue, while a delivered row is deleted
-- here. Rows are transient, so no indexes beyond the claim order are needed.
CREATE TABLE notification_outbox
(
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    -- SET NULL (not cascade): a purged actor must not drop a queued notification.
    actor_user_id   UUID        REFERENCES users (id) ON DELETE SET NULL,
    action          TEXT        NOT NULL,
    target_type     TEXT        NOT NULL,
    target_id       TEXT        NOT NULL,
    metadata        JSONB       NOT NULL DEFAULT '{}',
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_notification_outbox_occurred_at ON notification_outbox (occurred_at, id);

-- +goose Down
DROP TABLE notification_outbox;
