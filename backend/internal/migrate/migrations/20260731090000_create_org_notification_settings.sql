-- +goose Up
-- org_notification_settings is the per-organization subscription table: which
-- notification channels fire for which audit event. The subscriptions document
-- maps an audit action ("membership.invited") to the list of channel ids that
-- should be notified (["email", "slack"]); an action that is absent, or maps to
-- an empty list, notifies nobody. JSONB rather than a row per (event, channel)
-- because the set is edited and read as one document by one org admin screen,
-- and the dispatcher only ever looks it up whole by organization.
CREATE TABLE org_notification_settings
(
    organization_id UUID        PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    subscriptions   JSONB       NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE org_notification_settings;
