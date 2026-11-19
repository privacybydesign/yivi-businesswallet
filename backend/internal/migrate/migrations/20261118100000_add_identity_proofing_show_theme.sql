-- +goose Up
-- Whether the Idem app and hosted page wear the org's wallet theme; on keeps
-- what every org had.
ALTER TABLE identity_proofing_settings ADD COLUMN show_theme BOOLEAN NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE identity_proofing_settings DROP COLUMN show_theme;
