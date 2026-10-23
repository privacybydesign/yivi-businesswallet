-- +goose Up
-- A flow's people: the org's requests on one flow, newest first. The list
-- narrows by flow before its cap, so a quiet flow on a busy org is read from
-- its own rows rather than by walking every request of the org.
CREATE INDEX identity_proofing_requests_org_flow_idx
    ON identity_proofing_requests (organization_id, flow_id, created_at DESC);

-- +goose Down
DROP INDEX identity_proofing_requests_org_flow_idx;
