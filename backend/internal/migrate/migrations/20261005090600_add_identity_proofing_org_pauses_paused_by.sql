-- +goose Up
-- Who set each pause, so the pause shows by whom (not "paused by you" for
-- every platform admin). NULL for a pause set before this, or a user since
-- removed.
ALTER TABLE identity_proofing_org_pauses
    ADD COLUMN platform_paused_by UUID REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN org_paused_by      UUID REFERENCES users (id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE identity_proofing_org_pauses
    DROP COLUMN platform_paused_by,
    DROP COLUMN org_paused_by;
