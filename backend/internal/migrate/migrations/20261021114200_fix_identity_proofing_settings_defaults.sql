-- +goose Up
-- An org's QR lifetime and data retention are fixed defaults from now on, 10
-- minutes and 7 days, which each flow overrides in the flow editor
-- (sessionTtlSeconds, retentionOverrideSeconds); the API refuses a change
-- (proofing.checkSettingsChange). 20261021114000 and 20261021114100 copied the
-- org's own values into its flows and sent requests first, so none of them
-- changes.
UPDATE identity_proofing_settings
SET session_ttl_seconds = 600,
    data_retention_days = 7,
    updated_at          = now()
WHERE session_ttl_seconds <> 600
   OR data_retention_days <> 7;

ALTER TABLE identity_proofing_settings
    ALTER COLUMN data_retention_days SET DEFAULT 7;

-- +goose Down
-- The org's values become its own to set again, from the old default of 30
-- days. The values it had are not given back: its flows and sent requests
-- keep them as their own (20261021114000, 20261021114100), so each runs as it
-- did before the Up.
ALTER TABLE identity_proofing_settings
    ALTER COLUMN data_retention_days SET DEFAULT 30;
