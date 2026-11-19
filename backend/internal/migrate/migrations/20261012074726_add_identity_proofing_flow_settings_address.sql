-- +goose Up
-- Whether the subject enters their postal address after the identity check.
ALTER TABLE identity_proofing_flow_settings
    ADD COLUMN address TEXT NOT NULL DEFAULT 'off' CHECK (address IN ('off', 'required'));

-- +goose Down
ALTER TABLE identity_proofing_flow_settings DROP COLUMN address;
