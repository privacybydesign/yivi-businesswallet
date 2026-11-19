-- +goose Up
-- An org a platform admin put in test mode makes test proofing requests, and
-- sees only the requests of the mode it is in.
ALTER TABLE organizations ADD COLUMN test_mode BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE identity_proofing_requests ADD COLUMN test BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE identity_proofing_requests DROP COLUMN test;
ALTER TABLE organizations DROP COLUMN test_mode;
