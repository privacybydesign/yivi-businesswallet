-- +goose Up
-- identity_proofing_settings is what an org sets once for all its flows: how
-- long a session runs and how long a subject's data is kept, how the proofing
-- mail and the hosted pages look, and the origins a hosted page may redirect
-- its subject to and be embedded on. They were columns of the org's customers
-- (identity_proofing_customers), which are gone. No row reads as the defaults:
-- 10 minutes, 30 days, the org's own look and no redirect origins.
CREATE TABLE identity_proofing_settings
(
    organization_id          UUID PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    -- How long a session runs, counted from its creation (2, 5 or 10 minutes).
    session_ttl_seconds      INTEGER     NOT NULL DEFAULT 600 CHECK (session_ttl_seconds IN (120, 300, 600)),
    -- How long a session's personal data is kept after it ends before the
    -- pruner clears it. The app offers 7, 30, 90, 180 or 365
    -- (proofing.DataRetentionDayOptions); the database holds the bound.
    data_retention_days      INTEGER     NOT NULL DEFAULT 30 CHECK (data_retention_days BETWEEN 1 AND 365),
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
    -- Leaves the "powered by" line off the hosted pages.
    hide_powered_by          BOOLEAN     NOT NULL DEFAULT false,
    allowed_redirect_origins TEXT[]      NOT NULL DEFAULT '{}',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT identity_proofing_settings_logo_check CHECK ((logo_bytes IS NULL) = (logo_content_type IS NULL))
);

-- An org with customers keeps the settings of its oldest one. Requests already
-- sent keep the purge time they hold until their next write.
INSERT INTO identity_proofing_settings (organization_id, session_ttl_seconds, data_retention_days, display_name,
                                        primary_color, logo_bytes, logo_content_type, support_contact, privacy_url,
                                        hide_powered_by, allowed_redirect_origins)
SELECT DISTINCT ON (organization_id) organization_id,
                                     session_ttl_seconds,
                                     data_retention_days,
                                     display_name,
                                     primary_color,
                                     logo_bytes,
                                     logo_content_type,
                                     support_contact,
                                     privacy_url,
                                     hide_powered_by,
                                     allowed_redirect_origins
FROM identity_proofing_customers
ORDER BY organization_id, created_at, id;

-- +goose Down
DROP TABLE identity_proofing_settings;
