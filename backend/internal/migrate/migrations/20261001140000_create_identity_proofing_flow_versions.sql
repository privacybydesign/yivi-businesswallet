-- +goose Up
-- The org's identity-proofing flows, versioned (they lived at the
-- identity-proofing-service before the wallet ran proofing itself). A flow
-- version is immutable; exactly one version per flow is active and a
-- session pins the version active when it is created. The definition is the
-- whole configuration as JSON: steps, checks, thresholds, privacy policy.
CREATE TABLE identity_proofing_flow_versions (
    organization_id UUID NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    flow_id         TEXT NOT NULL,
    version         INTEGER NOT NULL CHECK (version > 0),
    definition      JSONB NOT NULL,
    active          BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, flow_id, version)
);

CREATE UNIQUE INDEX identity_proofing_flow_versions_active_idx
    ON identity_proofing_flow_versions (organization_id, flow_id) WHERE active;

-- +goose Down
DROP TABLE identity_proofing_flow_versions;
