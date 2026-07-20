-- +goose Up
-- Per-organization PostGuard behaviour that is neither the encryption key nor the
-- API key. Currently just the recipient-notification delivery method:
--   'postguard' — PostGuard's hosted mailing service notifies recipients (the
--                 default, and the behaviour before this setting existed).
--   'smtp'      — the backend sends the notification itself via the org's own SMTP
--                 configuration (internal/email), and PostGuard stays silent.
CREATE TABLE postguard_org_settings
(
    organization_id       UUID        PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    notification_delivery TEXT        NOT NULL DEFAULT 'postguard'
        CHECK (notification_delivery IN ('postguard', 'smtp')),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE postguard_org_settings;
