-- +goose Up
-- A Verzekeringsbericht with a current UWV benefit, or other current income
-- without hours worked, also sends an approved request to the wallet's
-- review: current_benefit, current_other_income.
ALTER TABLE identity_proofing_requests
    DROP CONSTRAINT identity_proofing_requests_review_reason_check,
    ADD CONSTRAINT identity_proofing_requests_review_reason_check
        CHECK (review_reason IN ('no_current_employment', 'current_benefit', 'current_other_income'));

-- +goose Down
-- Fails while such a review exists, rather than rewrite it.
ALTER TABLE identity_proofing_requests
    DROP CONSTRAINT identity_proofing_requests_review_reason_check,
    ADD CONSTRAINT identity_proofing_requests_review_reason_check
        CHECK (review_reason IN ('no_current_employment'));
