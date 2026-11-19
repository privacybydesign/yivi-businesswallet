-- +goose Up
-- Saved credential requests (issue #271's sending side): a named set of
-- credential types and claim names an organization asks other organizations
-- for again and again, so an admin picks it instead of retyping it. The
-- recipient is not part of a template: the same request goes to many parties.
--
-- credentials has the shape of openid4vp_outbound_requests.credentials, minus
-- the DCQL query ids, which a send assigns.
CREATE TABLE credential_request_templates
(
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name            TEXT        NOT NULL,
    credentials     JSONB       NOT NULL,
    created_by      UUID        REFERENCES users (id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One template per name within an organization, so the picker never shows
    -- two entries an admin cannot tell apart. Named so the store can map it.
    CONSTRAINT credential_request_templates_name_key UNIQUE (organization_id, name)
);

-- +goose Down
DROP TABLE credential_request_templates;
