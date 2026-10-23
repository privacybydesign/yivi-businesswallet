-- +goose Up
-- A Verzekeringsbericht with a current UWV benefit also sends an approved
-- request to the wallet's review: current_benefit.
ALTER TABLE identity_proofing_requests
    DROP CONSTRAINT identity_proofing_requests_review_reason_check,
    ADD CONSTRAINT identity_proofing_requests_review_reason_check
        CHECK (review_reason IN ('no_current_employment', 'current_benefit'));

-- +goose Down
UPDATE identity_proofing_requests SET review_reason = 'no_current_employment'
WHERE review_reason = 'current_benefit';
ALTER TABLE identity_proofing_requests
    DROP CONSTRAINT identity_proofing_requests_review_reason_check,
    ADD CONSTRAINT identity_proofing_requests_review_reason_check
        CHECK (review_reason IN ('no_current_employment'));
