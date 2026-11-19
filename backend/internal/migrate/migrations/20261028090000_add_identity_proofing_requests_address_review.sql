-- +goose Up
-- A self-declared address waits for a reviewer: pending until one confirms or
-- rejects it. The session's own outcome does not change.
ALTER TABLE identity_proofing_requests
    ADD COLUMN address_review TEXT CHECK (address_review IN ('pending', 'confirmed', 'rejected')),
    ADD COLUMN address_reviewed_by UUID REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN address_reviewed_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE identity_proofing_requests
    DROP COLUMN address_reviewed_at,
    DROP COLUMN address_reviewed_by,
    DROP COLUMN address_review;
