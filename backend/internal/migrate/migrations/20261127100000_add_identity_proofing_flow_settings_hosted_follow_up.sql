-- +goose Up
-- A flow that follows another for the same person: its hosted page's
-- consent screen leaves out what the first one already said.
ALTER TABLE identity_proofing_flow_settings
    ADD COLUMN hosted_follow_up BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE identity_proofing_flow_settings
    DROP COLUMN hosted_follow_up;
