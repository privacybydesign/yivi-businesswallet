-- +goose Up
-- Test keys are gone: every customer API key is a live one.
DELETE FROM identity_proofing_api_keys WHERE mode = 'test';
ALTER TABLE identity_proofing_api_keys DROP COLUMN mode;

-- +goose Down
ALTER TABLE identity_proofing_api_keys
    ADD COLUMN mode TEXT NOT NULL DEFAULT 'live' CHECK (mode IN ('live', 'test'));
