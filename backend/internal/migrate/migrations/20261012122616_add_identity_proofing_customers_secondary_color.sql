-- +goose Up
-- The customer's second colour in the Idem app (NULL is the org's accent
-- colour), next to primary_color.
ALTER TABLE identity_proofing_customers
    ADD COLUMN secondary_color TEXT CHECK (secondary_color ~ '^#[0-9a-fA-F]{6}$');

-- +goose Down
ALTER TABLE identity_proofing_customers
    DROP COLUMN secondary_color;
