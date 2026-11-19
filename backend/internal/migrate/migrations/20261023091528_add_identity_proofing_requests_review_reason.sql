-- +goose Up
-- Why the wallet itself put an approved request in review, after the engine
-- decided it, from its Verzekeringsbericht: no_current_employment (no current
-- job), current_benefit (a current UWV benefit beside a job) or
-- current_other_income (other current income without hours worked beside a
-- job). NULL for a review the engine opened, and for none.
ALTER TABLE identity_proofing_requests
    ADD COLUMN review_reason TEXT CHECK (review_reason IN ('no_current_employment', 'current_benefit', 'current_other_income'));

-- +goose Down
ALTER TABLE identity_proofing_requests DROP COLUMN review_reason;
