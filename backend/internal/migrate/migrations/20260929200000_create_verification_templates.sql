-- +goose Up
-- Org-scoped presentation request presets (issue #245): which credential type an
-- organisation asks a holder for, which claims, and the purpose shown to the
-- holder. A template is turned into a DCQL query when a verification starts.
CREATE TABLE verification_templates
(
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name            TEXT        NOT NULL,
    vct             TEXT        NOT NULL,
    claims          JSONB       NOT NULL DEFAULT '[]'::jsonb,
    purpose         TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_verification_templates_organization_id ON verification_templates (organization_id);

-- +goose Down
DROP TABLE verification_templates;
