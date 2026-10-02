-- +goose Up
-- The flows lived at the identity-proofing-service and were not carried
-- over: the wallet's engine starts with none (identity_proofing_flow_versions).
-- What the wallet kept per IPS flow id now names no flow, so it goes: the
-- members' allow-list, each customer's assignment, and the per-flow hosted
-- page and diploma settings. An org sets them again on the flows it creates.
-- Requests keep their flow id and name as history.
DELETE FROM org_identity_proofing_flows f
WHERE NOT EXISTS (SELECT 1 FROM identity_proofing_flow_versions v
                  WHERE v.organization_id = f.organization_id AND v.flow_id = f.flow_id);
DELETE FROM identity_proofing_customer_flows f
WHERE NOT EXISTS (SELECT 1 FROM identity_proofing_flow_versions v
                  WHERE v.organization_id = f.organization_id AND v.flow_id = f.flow_id);
DELETE FROM identity_proofing_flow_hosted_settings f
WHERE NOT EXISTS (SELECT 1 FROM identity_proofing_flow_versions v
                  WHERE v.organization_id = f.organization_id AND v.flow_id = f.flow_id);
DELETE FROM identity_proofing_flow_diploma_settings f
WHERE NOT EXISTS (SELECT 1 FROM identity_proofing_flow_versions v
                  WHERE v.organization_id = f.organization_id AND v.flow_id = f.flow_id);

-- +goose Down
-- The removed rows named IPS flows, which are gone: nothing to restore.
SELECT 1;
