-- +goose Up
-- An org has one default flow, the one a request form preselects. It was the
-- members' default, which had to be one of the flows members may use, and a
-- default per customer. Now it is one per org and independent of who may use
-- the flow; a data request flow cannot be it.
ALTER TABLE identity_proofing_flow_settings
    DROP CONSTRAINT identity_proofing_flow_settings_check;
ALTER TABLE identity_proofing_flow_settings
    RENAME COLUMN member_default TO is_default;
ALTER INDEX identity_proofing_flow_settings_one_member_default_idx
    RENAME TO identity_proofing_flow_settings_one_default_idx;

-- An org without a default takes the default of its oldest customer that had
-- one, unless that flow is a data request flow.
INSERT INTO identity_proofing_flow_settings (organization_id, flow_id, is_default)
SELECT d.organization_id, d.flow_id, true
FROM (SELECT DISTINCT ON (cf.organization_id) cf.organization_id, cf.flow_id
      FROM identity_proofing_customer_flows cf
               JOIN identity_proofing_customers c ON c.id = cf.customer_id
      WHERE cf.is_default
      ORDER BY cf.organization_id, c.created_at, c.id) d
WHERE NOT EXISTS (SELECT 1
                  FROM identity_proofing_flow_settings s
                  WHERE s.organization_id = d.organization_id
                    AND (s.is_default OR (s.flow_id = d.flow_id AND s.kind <> 'identity')))
ON CONFLICT (organization_id, flow_id) DO UPDATE SET is_default = true, updated_at = now();

ALTER TABLE identity_proofing_flow_settings
    ADD CONSTRAINT identity_proofing_flow_settings_default_kind_check CHECK (kind = 'identity' OR NOT is_default);

-- +goose Down
ALTER TABLE identity_proofing_flow_settings
    DROP CONSTRAINT identity_proofing_flow_settings_default_kind_check;

-- The members' default has to be a flow members may use.
UPDATE identity_proofing_flow_settings SET is_default = false WHERE is_default AND NOT member_allowed;

ALTER INDEX identity_proofing_flow_settings_one_default_idx
    RENAME TO identity_proofing_flow_settings_one_member_default_idx;
ALTER TABLE identity_proofing_flow_settings
    RENAME COLUMN is_default TO member_default;
ALTER TABLE identity_proofing_flow_settings
    ADD CONSTRAINT identity_proofing_flow_settings_check CHECK (member_allowed OR NOT member_default);
