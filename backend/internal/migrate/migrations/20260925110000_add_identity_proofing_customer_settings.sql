-- +goose Up
-- Per-customer session settings. session_ttl_seconds is how long a mailed
-- session runs, counted from the send (2, 5 or 10 minutes; 10 is IPS's own
-- default and what every request used before). data_retention_days is how long
-- the name read off an approved subject's document is kept before the pruner
-- clears it (7, 30 or 90 days; 30 was the fixed retention before).
ALTER TABLE identity_proofing_customers
    ADD COLUMN session_ttl_seconds INTEGER NOT NULL DEFAULT 600
        CHECK (session_ttl_seconds IN (120, 300, 600)),
    ADD COLUMN data_retention_days INTEGER NOT NULL DEFAULT 30
        CHECK (data_retention_days IN (7, 30, 90));

-- +goose Down
ALTER TABLE identity_proofing_customers
    DROP COLUMN data_retention_days,
    DROP COLUMN session_ttl_seconds;
