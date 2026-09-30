-- +goose Up
-- Identity proofing paused for an org: by a platform admin, or by the org's own
-- admin. Either pause stops it; no row is an org proofing as usual.
CREATE TABLE identity_proofing_org_pauses (
    organization_id    UUID PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    platform_paused_at TIMESTAMPTZ,
    org_paused_at      TIMESTAMPTZ,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE identity_proofing_org_pauses;
