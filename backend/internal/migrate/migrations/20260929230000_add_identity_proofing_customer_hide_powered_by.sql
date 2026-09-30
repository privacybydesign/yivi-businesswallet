-- +goose Up
-- A customer may leave the "powered by" line off its hosted pages.
ALTER TABLE identity_proofing_customers
    ADD COLUMN hide_powered_by BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE identity_proofing_customers
    DROP COLUMN hide_powered_by;
