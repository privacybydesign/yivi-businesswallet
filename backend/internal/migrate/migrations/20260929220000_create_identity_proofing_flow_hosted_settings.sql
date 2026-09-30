-- +goose Up
-- How a flow's hosted page behaves, per org (flows live at IPS): whether links
-- may be made for it, the languages it offers (empty: every supported one),
-- and whether it ends on the customer's redirect or its own thank-you page.
-- No row is the default: enabled, every language, redirect.
CREATE TABLE identity_proofing_flow_hosted_settings (
    organization_id UUID NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    flow_id         TEXT NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    locales         TEXT[] NOT NULL DEFAULT '{}',
    completion      TEXT NOT NULL DEFAULT 'redirect' CHECK (completion IN ('redirect', 'done')),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, flow_id)
);

-- +goose Down
DROP TABLE identity_proofing_flow_hosted_settings;
