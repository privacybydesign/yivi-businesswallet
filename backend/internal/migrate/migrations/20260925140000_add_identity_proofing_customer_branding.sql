-- +goose Up
-- A proofing customer's branding, which the proofing mail to its subjects
-- carries in place of the org's: the name it signs with (NULL is the
-- customer's name), its primary colour (NULL is the org's), its logo (NULL
-- shows the name as a wordmark), and a support contact and privacy statement
-- URL the mail adds (NULL leaves each out).
ALTER TABLE identity_proofing_customers
    ADD COLUMN display_name      TEXT CHECK (btrim(display_name) <> ''),
    ADD COLUMN primary_color     TEXT CHECK (primary_color ~ '^#[0-9a-fA-F]{6}$'),
    ADD COLUMN logo_bytes        BYTEA,
    ADD COLUMN logo_content_type TEXT,
    ADD COLUMN support_contact   TEXT CHECK (btrim(support_contact) <> ''),
    ADD COLUMN privacy_url       TEXT CHECK (privacy_url ~ '^https://'),
    ADD CHECK ((logo_bytes IS NULL) = (logo_content_type IS NULL));

-- +goose Down
ALTER TABLE identity_proofing_customers
    DROP COLUMN privacy_url,
    DROP COLUMN support_contact,
    DROP COLUMN logo_content_type,
    DROP COLUMN logo_bytes,
    DROP COLUMN primary_color,
    DROP COLUMN display_name;
