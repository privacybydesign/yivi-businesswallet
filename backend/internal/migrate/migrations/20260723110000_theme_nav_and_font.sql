-- +goose Up
-- Extend per-organization theming so a tenant can also colour its navigation
-- chrome (sidebar and top bar) and pick a body font. sidebar_color and
-- topbar_color are CSS hex strings (e.g. "#1d4e89"); font_family holds a CSS
-- font-family list string (e.g. "Open Sans", sans-serif), NOT a colour. Empty
-- string means "unset": fall back to the default look, exactly like the other
-- theme columns.
ALTER TABLE org_theme_settings
    ADD COLUMN sidebar_color TEXT NOT NULL DEFAULT '', -- sidebar/navigation chrome token
    ADD COLUMN topbar_color  TEXT NOT NULL DEFAULT '', -- top bar/navigation chrome token
    ADD COLUMN font_family   TEXT NOT NULL DEFAULT ''; -- CSS font-family list string (not a colour)

-- +goose Down
ALTER TABLE org_theme_settings
    DROP COLUMN sidebar_color,
    DROP COLUMN topbar_color,
    DROP COLUMN font_family;
