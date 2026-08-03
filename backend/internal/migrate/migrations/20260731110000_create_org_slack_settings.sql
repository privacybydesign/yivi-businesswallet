-- +goose Up
-- Per-organization Slack incoming-webhook configuration for the Slack
-- notification channel. The webhook URL is the workspace's secret — it is the
-- whole credential needed to post as the integration — so it is stored encrypted
-- at rest (AES-256-GCM under the deployment Slack encryption key) and the row
-- never holds the plaintext URL. `enabled` pauses delivery without discarding the
-- URL; a row with no ciphertext, or a disabled one, delivers nothing.
CREATE TABLE org_slack_settings
(
    organization_id        UUID        PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    webhook_url_ciphertext BYTEA,
    enabled                BOOLEAN     NOT NULL DEFAULT false,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE org_slack_settings;
