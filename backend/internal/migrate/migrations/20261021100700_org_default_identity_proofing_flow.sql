-- +goose Up
-- An org has one default flow: the one an API call that names no flow runs, and
-- the one a request form preselects. It was two: the members' default, which
-- only preselected a flow in the form for a member and had to be one members
-- may use, and a default per customer, which a call with that customer's API
-- key ran when it named no flow. Now it is one per org and independent of who
-- may use the flow; a data request flow cannot be it.
ALTER TABLE identity_proofing_flow_settings
    DROP CONSTRAINT identity_proofing_flow_settings_check;
ALTER TABLE identity_proofing_flow_settings
    RENAME COLUMN member_default TO is_default;
ALTER INDEX identity_proofing_flow_settings_one_member_default_idx
    RENAME TO identity_proofing_flow_settings_one_default_idx;

-- A customer's default wins over the members' one: an integration that names
-- no flow must keep running the flow it ran, while a sender in the app sees the
-- preselected flow and can pick another. It is the default of the oldest
-- customer whose key still works after the migration (an API key not revoked,
-- of a customer not paused: the migration for the keys revokes a paused
-- customer's), else of the oldest customer with one. A data request flow
-- cannot be the org's default, so a customer whose default was one is passed
-- over. An org whose customers' keys ran different defaults keeps one: a call
-- with another customer's key that names no flow now runs it. The members'
-- form preselects the org's default when members may use it, else the first
-- flow they may use.
UPDATE identity_proofing_flow_settings s
SET is_default = false,
    updated_at = now()
WHERE s.is_default
  AND EXISTS (SELECT 1
              FROM identity_proofing_customer_flows cf
              WHERE cf.organization_id = s.organization_id
                AND cf.is_default
                AND NOT EXISTS (SELECT 1
                                FROM identity_proofing_flow_settings fs
                                WHERE fs.organization_id = cf.organization_id
                                  AND fs.flow_id = cf.flow_id
                                  AND fs.kind <> 'identity'));

INSERT INTO identity_proofing_flow_settings (organization_id, flow_id, is_default)
SELECT DISTINCT ON (cf.organization_id) cf.organization_id, cf.flow_id, true
FROM identity_proofing_customer_flows cf
         JOIN identity_proofing_customers c ON c.id = cf.customer_id
WHERE cf.is_default
  AND NOT EXISTS (SELECT 1
                  FROM identity_proofing_flow_settings fs
                  WHERE fs.organization_id = cf.organization_id
                    AND fs.flow_id = cf.flow_id
                    AND fs.kind <> 'identity')
ORDER BY cf.organization_id,
         (c.paused_at IS NULL AND EXISTS (SELECT 1
                                          FROM identity_proofing_api_keys ak
                                          WHERE ak.customer_id = c.id
                                            AND ak.revoked_at IS NULL)) DESC,
         c.created_at,
         c.id
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
