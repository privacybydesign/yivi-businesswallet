-- +goose Up
-- identity_proofing_settings is what an org sets once for all its flows: how
-- long a session runs and how long a subject's data is kept, how "delete my
-- data" requests are decided, how the proofing mail, the hosted pages and the
-- Idem app look, and the origins a hosted page may redirect its subject to and
-- be embedded on. They were columns of the org's customers
-- (identity_proofing_customers), which are gone. No row reads as the defaults:
-- 10 minutes, 30 days, erasure by review, the org's own look and no redirect
-- origins.
CREATE TABLE identity_proofing_settings
(
    organization_id          UUID PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    -- How long a session runs, counted from its creation (2, 5 or 10 minutes).
    session_ttl_seconds      INTEGER     NOT NULL DEFAULT 600 CHECK (session_ttl_seconds IN (120, 300, 600)),
    -- How long a session's personal data is kept after it ends before the
    -- pruner clears it. The app offers 7, 30, 90, 180 or 365
    -- (proofing.DataRetentionDayOptions); the database holds the bound.
    data_retention_days      INTEGER     NOT NULL DEFAULT 30 CHECK (data_retention_days BETWEEN 1 AND 365),
    -- How "delete my data" requests are decided once the person is proven:
    -- 'review' by a reviewer; 'automatic' erases the sessions proven to be the
    -- person's at once, and leaves a reviewer only those matched on an e-mail
    -- address or typed name; 'keep' refuses every one, for an org bound by law
    -- to keep the data (LEGAL_RETENTION).
    erasure_handling         TEXT        NOT NULL DEFAULT 'review'
        CHECK (erasure_handling IN ('review', 'automatic', 'keep')),
    -- The name the proofing mail signs with (NULL is the org's name), its
    -- primary colour (NULL is the org's), its logo (NULL shows the name as a
    -- wordmark), and a support contact and privacy statement URL (NULL leaves
    -- each out).
    display_name             TEXT CHECK (btrim(display_name) <> ''),
    primary_color            TEXT CHECK (primary_color ~ '^#[0-9a-fA-F]{6}$'),
    logo_bytes               BYTEA,
    logo_content_type        TEXT,
    support_contact          TEXT CHECK (btrim(support_contact) <> ''),
    privacy_url              TEXT CHECK (privacy_url ~ '^https://'),
    -- The rest of the white-label the Idem app and the hosted page take, the
    -- same palette and font as the org theme (org_theme_settings) has; NULL
    -- keeps the design's own.
    secondary_color          TEXT CHECK (secondary_color ~ '^#[0-9a-fA-F]{6}$'),
    text_color               TEXT CHECK (text_color ~ '^#[0-9a-fA-F]{6}$'),
    surface_color            TEXT CHECK (surface_color ~ '^#[0-9a-fA-F]{6}$'),
    border_color             TEXT CHECK (border_color ~ '^#[0-9a-fA-F]{6}$'),
    link_color               TEXT CHECK (link_color ~ '^#[0-9a-fA-F]{6}$'),
    success_color            TEXT CHECK (success_color ~ '^#[0-9a-fA-F]{6}$'),
    warning_color            TEXT CHECK (warning_color ~ '^#[0-9a-fA-F]{6}$'),
    error_color              TEXT CHECK (error_color ~ '^#[0-9a-fA-F]{6}$'),
    topbar_color             TEXT CHECK (topbar_color ~ '^#[0-9a-fA-F]{6}$'),
    font_family              TEXT CHECK (font_family ~ '^[A-Za-z0-9 ,''"-]{1,120}$'),
    -- Leaves the "powered by" line off the hosted pages.
    hide_powered_by          BOOLEAN     NOT NULL DEFAULT false,
    allowed_redirect_origins TEXT[]      NOT NULL DEFAULT '{}',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT identity_proofing_settings_logo_check CHECK ((logo_bytes IS NULL) = (logo_content_type IS NULL))
);

-- An org with customers keeps the settings of its oldest one. Requests already
-- sent keep the purge time they hold until their next write.
INSERT INTO identity_proofing_settings (organization_id, session_ttl_seconds, data_retention_days, erasure_handling,
                                        display_name, primary_color, logo_bytes, logo_content_type, support_contact,
                                        privacy_url, secondary_color, text_color, surface_color, border_color,
                                        link_color, success_color, warning_color, error_color, topbar_color,
                                        font_family, hide_powered_by, allowed_redirect_origins)
SELECT DISTINCT ON (organization_id) organization_id,
                                     session_ttl_seconds,
                                     data_retention_days,
                                     erasure_handling,
                                     display_name,
                                     primary_color,
                                     logo_bytes,
                                     logo_content_type,
                                     support_contact,
                                     privacy_url,
                                     secondary_color,
                                     text_color,
                                     surface_color,
                                     border_color,
                                     link_color,
                                     success_color,
                                     warning_color,
                                     error_color,
                                     topbar_color,
                                     font_family,
                                     hide_powered_by,
                                     allowed_redirect_origins
FROM identity_proofing_customers
ORDER BY organization_id, created_at, id;

-- +goose Down
DROP TABLE identity_proofing_settings;
