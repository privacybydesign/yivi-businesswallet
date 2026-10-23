-- +goose Up
-- Why the wallet itself put an approved request in review, after the engine
-- decided it: no_current_employment, a Verzekeringsbericht that lists no
-- current job. NULL for a review the engine opened, and for none.
ALTER TABLE identity_proofing_requests
    ADD COLUMN review_reason TEXT CHECK (review_reason IN ('no_current_employment'));

-- +goose Down
ALTER TABLE identity_proofing_requests DROP COLUMN review_reason;
