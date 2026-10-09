-- +goose Up
-- Test mode is gone: a test key's scripted requests had no subject, so they go
-- with it, and every request runs for real.
DELETE FROM identity_proofing_requests WHERE mode = 'test';
ALTER TABLE identity_proofing_requests DROP COLUMN mode;

-- +goose Down
ALTER TABLE identity_proofing_requests
    ADD COLUMN mode TEXT NOT NULL DEFAULT 'live' CHECK (mode IN ('live', 'test'));
