-- +goose Up
-- The kept diploma extracts get their own review item, on a flow that sends
-- them to review.
ALTER TABLE identity_proofing_review_items
    DROP CONSTRAINT identity_proofing_review_items_kind_check,
    ADD CONSTRAINT identity_proofing_review_items_kind_check
        CHECK (kind IN ('identity', 'address', 'employment', 'diplomas', 'data_request'));

-- +goose Down
DELETE FROM identity_proofing_review_items WHERE kind = 'diplomas';
ALTER TABLE identity_proofing_review_items
    DROP CONSTRAINT identity_proofing_review_items_kind_check,
    ADD CONSTRAINT identity_proofing_review_items_kind_check
        CHECK (kind IN ('identity', 'address', 'employment', 'data_request'));
