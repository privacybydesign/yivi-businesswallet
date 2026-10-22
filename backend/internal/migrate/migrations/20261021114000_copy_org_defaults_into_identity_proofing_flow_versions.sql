-- +goose Up
-- An org's QR lifetime and data retention stop being its own to set: they
-- become fixed defaults of 10 minutes and 7 days (20261021114200), and each
-- flow sets its own in the flow editor. So that no flow changes, an active
-- flow version that sets neither gets a new active version carrying the org's
-- current value, where that is not the fixed default: the same definition
-- otherwise, under the flow's next version number. An org that saved no
-- settings had 30 days (proofing.proofedNameRetention) and 10 minutes. The
-- definition holds both as Go durations, in nanoseconds (FlowDefinition's
-- RetentionOverride and SessionTTL). Requests already sent keep the version
-- they were sent on.
CREATE TEMPORARY TABLE flow_org_default_copies AS
WITH org AS (SELECT o.id                                  AS organization_id,
                    COALESCE(st.data_retention_days, 30)  AS retention_days,
                    COALESCE(st.session_ttl_seconds, 600) AS ttl_seconds
             FROM organizations o
                      LEFT JOIN identity_proofing_settings st ON st.organization_id = o.id),
     active AS (SELECT v.organization_id,
                       v.flow_id,
                       v.definition,
                       org.retention_days,
                       org.ttl_seconds,
                       COALESCE((v.definition ->> 'RetentionOverride')::bigint, 0) = 0
                           AND org.retention_days <> 7 AS copy_retention,
                       COALESCE((v.definition ->> 'SessionTTL')::bigint, 0) = 0
                           AND org.ttl_seconds <> 600  AS copy_ttl
                FROM identity_proofing_flow_versions v
                         JOIN org USING (organization_id)
                WHERE v.active)
SELECT a.organization_id,
       a.flow_id,
       (SELECT max(m.version) + 1
        FROM identity_proofing_flow_versions m
        WHERE m.organization_id = a.organization_id
          AND m.flow_id = a.flow_id) AS version,
       a.definition
           || CASE
                  WHEN a.copy_retention
                      -- days * seconds per day * nanoseconds per second
                      THEN jsonb_build_object('RetentionOverride', a.retention_days::bigint * 86400 * 1000000000)
                  ELSE '{}'::jsonb END
           || CASE
                  WHEN a.copy_ttl
                      THEN jsonb_build_object('SessionTTL', a.ttl_seconds::bigint * 1000000000)
                  ELSE '{}'::jsonb END AS definition
FROM active a
WHERE a.copy_retention
   OR a.copy_ttl;

UPDATE identity_proofing_flow_versions v
SET active = false
FROM flow_org_default_copies c
WHERE v.organization_id = c.organization_id
  AND v.flow_id = c.flow_id
  AND v.active;

INSERT INTO identity_proofing_flow_versions (organization_id, flow_id, version, definition, active, created_at)
SELECT organization_id,
       flow_id,
       version,
       definition || jsonb_build_object('Version', version, 'Active', true, 'CreatedAt', now()),
       true,
       now()
FROM flow_org_default_copies;

DROP TABLE flow_org_default_copies;

-- +goose Down
-- Intentionally empty. The copied versions stay active: they carry the values
-- the org had, which 20261021114200's Down does not give back to the org, so
-- dropping them would change how each flow runs. Requests sent since name them.
