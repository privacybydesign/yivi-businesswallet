-- +goose Up
-- org_onboarding_attestations is the per-organization set of attestation
-- templates automatically issued to a new member when they accept an invitation
-- (onboarding). It replaces the hardcoded member-invite UI stub: presence of a
-- row means "auto-issue this template on onboarding", and position orders the
-- set as the admin arranged it. Templates are org-scoped, so the referenced
-- template is always one of the organization's own; ON DELETE CASCADE drops the
-- binding when the template (or organization) is deleted.
CREATE TABLE org_onboarding_attestations
(
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    template_id     UUID        NOT NULL REFERENCES attestation_templates (id) ON DELETE CASCADE,
    position        INT         NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, template_id)
);

CREATE INDEX idx_org_onboarding_attestations_org
    ON org_onboarding_attestations (organization_id, position);

-- +goose Down
DROP TABLE org_onboarding_attestations;
