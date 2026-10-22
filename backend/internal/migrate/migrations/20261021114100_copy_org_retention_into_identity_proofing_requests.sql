-- +goose Up
-- A request to an external person that its flow gave no retention followed
-- the org's (setPurgeAt), which 20261021114200 fixes at 7 days. Such a request
-- keeps the org's current retention as its own, as its flow does
-- (20261021114000), so neither its purge nor the engine's copy of its session
-- (syncEngineRetention) comes sooner than its person was told. An org without
-- a settings row is on the 7 days already (20261021110200). A member's
-- request follows proofedNameRetention, not the org's, and is left as it is.
-- Its purge_at stays: the same retention, now its own.
UPDATE identity_proofing_requests r
SET retention_override_seconds = org.retention_days * 86400 -- seconds per day
FROM (SELECT o.id AS organization_id, COALESCE(st.data_retention_days, 7) AS retention_days
      FROM organizations o
               LEFT JOIN identity_proofing_settings st ON st.organization_id = o.id) org
WHERE r.organization_id = org.organization_id
  AND r.subject_kind = 'external'
  AND r.purged_at IS NULL
  AND r.retention_override_seconds IS NULL
  AND org.retention_days <> 7;

-- +goose Down
-- Intentionally empty. 20261021114200's Down does not give the org back the
-- retention it had, so a request without this one of its own would be
-- purged sooner than its person was told.
