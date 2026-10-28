-- +goose Up
-- See my data and Delete my data are a standard feature of every org, not
-- flows an admin sets up: the wallet creates one flow of each data request
-- kind per org, on the wallet's own definition, the first time it is needed
-- (proofing.Service.standardFlows). standard marks that flow: every data
-- request of its kind runs on it. No admin edits it, and no flow can be made
-- a data request any more.
ALTER TABLE identity_proofing_flow_settings
    ADD COLUMN standard BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE identity_proofing_flow_settings
    ADD CONSTRAINT identity_proofing_flow_settings_standard_kind_check CHECK (kind <> 'identity' OR NOT standard);

CREATE UNIQUE INDEX identity_proofing_flow_settings_one_standard_idx
    ON identity_proofing_flow_settings (organization_id, kind) WHERE standard;

-- An org that already has flows of a data request kind keeps the oldest as its
-- standard one: the one whose first version the engine created first, else (a
-- flow the engine does not hold, as under the in-memory stub) the one whose
-- settings were written first. The others stay data request flows: hidden
-- like the standard one, not editable, and a request that names one runs on
-- the standard flow of its kind. Their past requests keep pointing at them.
UPDATE identity_proofing_flow_settings s
SET standard   = true,
    updated_at = now()
FROM (SELECT DISTINCT ON (s.organization_id, s.kind) s.organization_id, s.flow_id
      FROM identity_proofing_flow_settings s
               LEFT JOIN (SELECT organization_id, flow_id, MIN(created_at) AS created_at
                          FROM identity_proofing_flow_versions
                          GROUP BY organization_id, flow_id) v
                         ON v.organization_id = s.organization_id AND v.flow_id = s.flow_id
      WHERE s.kind IN ('data_access', 'data_erasure')
      ORDER BY s.organization_id, s.kind, v.created_at IS NULL, COALESCE(v.created_at, s.created_at), s.flow_id) oldest
WHERE s.organization_id = oldest.organization_id
  AND s.flow_id = oldest.flow_id;

-- Nobody can open a data request flow's Hosted page tab any more, so one an
-- admin switched off would stay off: every data request flow's hosted page goes
-- back to the default (on, in every language, ending on the request's
-- redirect). Down does not switch them off again.
UPDATE identity_proofing_flow_settings
SET hosted_enabled    = true,
    hosted_locales    = '{}',
    hosted_completion = 'redirect',
    updated_at        = now()
WHERE kind IN ('data_access', 'data_erasure')
  AND (NOT hosted_enabled OR hosted_locales <> '{}' OR hosted_completion <> 'redirect');

-- +goose Down
DROP INDEX identity_proofing_flow_settings_one_standard_idx;
ALTER TABLE identity_proofing_flow_settings
    DROP CONSTRAINT identity_proofing_flow_settings_standard_kind_check;
ALTER TABLE identity_proofing_flow_settings
    DROP COLUMN standard;
