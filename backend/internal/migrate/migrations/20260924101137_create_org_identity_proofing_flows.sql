-- +goose Up
-- org_identity_proofing_flows is the org admin's allow-list over the org's IPS
-- flows: the flows members may send a proofing request on, with exactly one
-- default the request form preselects. The flows themselves live at IPS; this
-- table holds only their ids. An id IPS no longer lists (a deactivated flow) is
-- ignored on read. No row means members have no flow to send on yet.
CREATE TABLE org_identity_proofing_flows
(
    organization_id UUID        NOT NULL REFERENCES org_identity_proofing_settings (organization_id) ON DELETE CASCADE,
    flow_id         TEXT        NOT NULL,
    is_default      BOOLEAN     NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, flow_id)
);

CREATE UNIQUE INDEX org_identity_proofing_flows_one_default_idx
    ON org_identity_proofing_flows (organization_id) WHERE is_default;

-- +goose Down
DROP TABLE org_identity_proofing_flows;
