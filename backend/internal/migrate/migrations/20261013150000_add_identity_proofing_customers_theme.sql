-- +goose Up
-- The rest of a customer's white-label, the same palette and font as the org
-- theme (org_theme_settings) has; NULL keeps the design's own.
ALTER TABLE identity_proofing_customers
    ADD COLUMN text_color    TEXT CHECK (text_color ~ '^#[0-9a-fA-F]{6}$'),
    ADD COLUMN surface_color TEXT CHECK (surface_color ~ '^#[0-9a-fA-F]{6}$'),
    ADD COLUMN border_color  TEXT CHECK (border_color ~ '^#[0-9a-fA-F]{6}$'),
    ADD COLUMN link_color    TEXT CHECK (link_color ~ '^#[0-9a-fA-F]{6}$'),
    ADD COLUMN success_color TEXT CHECK (success_color ~ '^#[0-9a-fA-F]{6}$'),
    ADD COLUMN warning_color TEXT CHECK (warning_color ~ '^#[0-9a-fA-F]{6}$'),
    ADD COLUMN error_color   TEXT CHECK (error_color ~ '^#[0-9a-fA-F]{6}$'),
    ADD COLUMN topbar_color  TEXT CHECK (topbar_color ~ '^#[0-9a-fA-F]{6}$'),
    ADD COLUMN font_family   TEXT CHECK (font_family ~ '^[A-Za-z0-9 ,''"-]{1,120}$');

-- +goose Down
ALTER TABLE identity_proofing_customers
    DROP COLUMN text_color,
    DROP COLUMN surface_color,
    DROP COLUMN border_color,
    DROP COLUMN link_color,
    DROP COLUMN success_color,
    DROP COLUMN warning_color,
    DROP COLUMN error_color,
    DROP COLUMN topbar_color,
    DROP COLUMN font_family;
