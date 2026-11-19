-- +goose Up
-- Whether the subject uploads their UWV Verzekeringsbericht after the identity
-- check.
ALTER TABLE identity_proofing_flow_settings
    ADD COLUMN employment TEXT NOT NULL DEFAULT 'off' CHECK (employment IN ('off', 'required'));

-- +goose Down
ALTER TABLE identity_proofing_flow_settings DROP COLUMN employment;
