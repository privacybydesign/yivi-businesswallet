-- +goose Up
-- Extend per-organization theming from two colours (primary/accent) to the full
-- palette, so a tenant can re-colour text, surfaces, borders, links and the
-- status tokens too. Each is a CSS hex string (e.g. "#1d4e89"); empty string
-- means "unset": fall back to the default look, exactly like primary_color and
-- accent_color.
ALTER TABLE org_theme_settings
    ADD COLUMN text_color    TEXT NOT NULL DEFAULT '', -- body/foreground text token
    ADD COLUMN surface_color TEXT NOT NULL DEFAULT '', -- surface/background token
    ADD COLUMN border_color  TEXT NOT NULL DEFAULT '', -- border/divider token
    ADD COLUMN link_color    TEXT NOT NULL DEFAULT '', -- hyperlink token
    ADD COLUMN success_color TEXT NOT NULL DEFAULT '', -- success status token
    ADD COLUMN warning_color TEXT NOT NULL DEFAULT '', -- warning status token
    ADD COLUMN error_color   TEXT NOT NULL DEFAULT ''; -- error status token

-- +goose Down
ALTER TABLE org_theme_settings
    DROP COLUMN text_color,
    DROP COLUMN surface_color,
    DROP COLUMN border_color,
    DROP COLUMN link_color,
    DROP COLUMN success_color,
    DROP COLUMN warning_color,
    DROP COLUMN error_color;
