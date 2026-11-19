-- +goose Up
-- When the identity check finished: the engine approved it or opened a review.
-- The steps after it (address, diplomas, Verzekeringsbericht) take input from
-- then for their window, so the subject gives everything before a reviewer
-- decides. Set once; a review's decision moves completed_at, not this.
ALTER TABLE identity_proofing_requests ADD COLUMN checked_at TIMESTAMPTZ;

UPDATE identity_proofing_requests SET checked_at = completed_at
    WHERE status IN ('approved', 'needs_review') AND completed_at IS NOT NULL;

-- +goose Down
ALTER TABLE identity_proofing_requests DROP COLUMN checked_at;
