-- +goose Up
-- Per-organization Veramo issuer instance. Each org issues attestations from its
-- own issuer instance (the {instance} path segment at the hosted issuer), letting
-- the credential carry the org's own display name / branding. The shared admin
-- token is deployment-global (rendered into every instance's config), so no
-- secret is stored here — only the instance name and branding. instance_name is
-- the {instance} segment offers route to; display_name / logo_uri feed the
-- generated issuer metadata's `display` (the wallet's issuer branding).
CREATE TABLE org_issuer_settings
(
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL UNIQUE REFERENCES organizations (id) ON DELETE CASCADE,
    instance_name   TEXT        NOT NULL,
    display_name    TEXT        NOT NULL DEFAULT '',
    logo_uri        TEXT        NOT NULL DEFAULT '',
    enabled         BOOLEAN     NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE org_issuer_settings;
