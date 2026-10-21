-- +goose Up
-- Customers are gone: flows are the org's main object, and what a customer held
-- is the org's (identity_proofing_settings, API keys, the webhook endpoint) or
-- the flow's. Its sessions stay, as external requests of the org.
DROP TABLE identity_proofing_customers;

-- +goose Down
CREATE TABLE identity_proofing_customers
(
    id                       UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    organization_id          UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name                     TEXT        NOT NULL CHECK (btrim(name) <> ''),
    created_by               UUID        REFERENCES users (id) ON DELETE SET NULL,
    paused_at                TIMESTAMPTZ,
    session_ttl_seconds      INTEGER     NOT NULL DEFAULT 600 CHECK (session_ttl_seconds IN (120, 300, 600)),
    data_retention_days      INTEGER     NOT NULL DEFAULT 30 CHECK (data_retention_days BETWEEN 1 AND 365),
    erasure_handling         TEXT        NOT NULL DEFAULT 'review'
        CHECK (erasure_handling IN ('review', 'automatic', 'keep')),
    display_name             TEXT CHECK (btrim(display_name) <> ''),
    primary_color            TEXT CHECK (primary_color ~ '^#[0-9a-fA-F]{6}$'),
    logo_bytes               BYTEA,
    logo_content_type        TEXT,
    support_contact          TEXT CHECK (btrim(support_contact) <> ''),
    privacy_url              TEXT CHECK (privacy_url ~ '^https://'),
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
    hide_powered_by          BOOLEAN     NOT NULL DEFAULT false,
    allowed_redirect_origins TEXT[]      NOT NULL DEFAULT '{}',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, id),
    CONSTRAINT identity_proofing_customers_check CHECK ((logo_bytes IS NULL) = (logo_content_type IS NULL))
);

CREATE UNIQUE INDEX identity_proofing_customers_org_name_idx
    ON identity_proofing_customers (organization_id, lower(name));

-- One customer, named after the org, for every org that holds anything a
-- customer owned, with the org's settings.
INSERT INTO identity_proofing_customers (organization_id, name, session_ttl_seconds, data_retention_days,
                                         erasure_handling, display_name, primary_color, logo_bytes,
                                         logo_content_type, support_contact, privacy_url, secondary_color,
                                         text_color, surface_color, border_color, link_color, success_color,
                                         warning_color, error_color, topbar_color, font_family, hide_powered_by,
                                         allowed_redirect_origins)
SELECT o.id,
       o.name,
       COALESCE(s.session_ttl_seconds, 600),
       COALESCE(s.data_retention_days, 30),
       COALESCE(s.erasure_handling, 'review'),
       s.display_name,
       s.primary_color,
       s.logo_bytes,
       s.logo_content_type,
       s.support_contact,
       s.privacy_url,
       s.secondary_color,
       s.text_color,
       s.surface_color,
       s.border_color,
       s.link_color,
       s.success_color,
       s.warning_color,
       s.error_color,
       s.topbar_color,
       s.font_family,
       COALESCE(s.hide_powered_by, false),
       COALESCE(s.allowed_redirect_origins, '{}')
FROM organizations o
         LEFT JOIN identity_proofing_settings s ON s.organization_id = o.id
WHERE s.organization_id IS NOT NULL
   OR EXISTS (SELECT 1 FROM identity_proofing_api_keys k WHERE k.organization_id = o.id)
   OR EXISTS (SELECT 1 FROM identity_proofing_webhooks w WHERE w.organization_id = o.id)
   OR EXISTS (SELECT 1 FROM identity_proofing_webhook_deliveries d WHERE d.organization_id = o.id)
   OR EXISTS (SELECT 1
              FROM identity_proofing_requests r
              WHERE r.organization_id = o.id
                AND r.subject_kind = 'external');
