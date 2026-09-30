-- +goose Up
-- The design's retention: default 30 days, at most 365. The app offers 7, 30,
-- 90, 180 or 365 (proofing.DataRetentionDayOptions); the database holds the bound.
ALTER TABLE identity_proofing_customers
    DROP CONSTRAINT identity_proofing_customers_data_retention_days_check,
    ADD CONSTRAINT identity_proofing_customers_data_retention_days_check
        CHECK (data_retention_days BETWEEN 1 AND 365);

-- +goose Down
UPDATE identity_proofing_customers SET data_retention_days = 90 WHERE data_retention_days > 90;
ALTER TABLE identity_proofing_customers
    DROP CONSTRAINT identity_proofing_customers_data_retention_days_check,
    ADD CONSTRAINT identity_proofing_customers_data_retention_days_check
        CHECK (data_retention_days IN (7, 30, 90));
