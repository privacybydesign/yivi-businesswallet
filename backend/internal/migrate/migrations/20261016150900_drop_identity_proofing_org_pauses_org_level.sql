-- +goose Up
-- An org's own admin can no longer pause its proofing; only a platform admin
-- can. A pause the org had set is lifted, as nobody could lift it any more.
ALTER TABLE identity_proofing_org_pauses
    DROP COLUMN org_paused_at,
    DROP COLUMN org_paused_by;

DELETE FROM identity_proofing_org_pauses WHERE platform_paused_at IS NULL;

-- +goose Down
ALTER TABLE identity_proofing_org_pauses
    ADD COLUMN org_paused_at TIMESTAMPTZ,
    ADD COLUMN org_paused_by UUID REFERENCES users (id) ON DELETE SET NULL;
