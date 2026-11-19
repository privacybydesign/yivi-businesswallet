-- +goose Up
-- Only a platform admin pauses an org's proofing now: the org's own admin can
-- no longer pause it, and nothing below the org (a customer) can be paused.
-- What was paused stays paused, as a platform pause, which only a platform
-- admin lifts:
-- - an org its own admin had paused, since they did;
-- - an org whose every customer was paused, since the last of them was. A
--   customer paused while others were not loses its API keys instead (the
--   migration for the keys).
-- A platform pause already set keeps its time and who set it. One carried over
-- names nobody, so it reads as the platform's. A review still open in an org
-- paused here is rejected by the hourly sweep (rejectPausedReviews), as one
-- left over by a pause set through the API is.
UPDATE identity_proofing_org_pauses
SET platform_paused_at = org_paused_at,
    updated_at         = now()
WHERE platform_paused_at IS NULL
  AND org_paused_at IS NOT NULL;

INSERT INTO identity_proofing_org_pauses (organization_id, platform_paused_at)
SELECT organization_id, max(paused_at)
FROM identity_proofing_customers
GROUP BY organization_id
HAVING bool_and(paused_at IS NOT NULL)
ON CONFLICT (organization_id) DO UPDATE
    SET platform_paused_at = EXCLUDED.platform_paused_at,
        updated_at         = now()
WHERE identity_proofing_org_pauses.platform_paused_at IS NULL;

ALTER TABLE identity_proofing_org_pauses
    DROP COLUMN org_paused_at,
    DROP COLUMN org_paused_by;

DELETE FROM identity_proofing_org_pauses WHERE platform_paused_at IS NULL;

-- +goose Down
-- A pause carried over stays a platform pause.
ALTER TABLE identity_proofing_org_pauses
    ADD COLUMN org_paused_at TIMESTAMPTZ,
    ADD COLUMN org_paused_by UUID REFERENCES users (id) ON DELETE SET NULL;
