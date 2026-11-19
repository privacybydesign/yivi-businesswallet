-- +goose Up
-- A new org purges personal data a week after a check ends, as Settings ›
-- Data and privacy says (proofing.defaultDataRetentionDays). An org without a
-- settings row reads that default too, so one that already sent an external
-- request gets a row first with the 30 days it had: its retention does not
-- change under it.
INSERT INTO identity_proofing_settings (organization_id, data_retention_days)
SELECT DISTINCT r.organization_id, 30
FROM identity_proofing_requests r
WHERE r.subject_kind = 'external'
ON CONFLICT (organization_id) DO NOTHING;

ALTER TABLE identity_proofing_settings
    ALTER COLUMN data_retention_days SET DEFAULT 7;

-- +goose Down
-- The rows Up added hold what the old default gave them, so they stay.
ALTER TABLE identity_proofing_settings
    ALTER COLUMN data_retention_days SET DEFAULT 30;
