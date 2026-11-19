-- +goose Up
-- An event about a test proofing session (made while its org was in test mode).
ALTER TABLE audit_events ADD COLUMN test BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE audit_events DROP COLUMN test;
