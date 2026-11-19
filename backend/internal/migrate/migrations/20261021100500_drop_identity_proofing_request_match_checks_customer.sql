-- +goose Up
-- What a data request found per candidate stays within one org, which is all
-- that kept it within one customer now. Dropping the column drops the
-- composite foreign keys.
ALTER TABLE identity_proofing_request_match_checks
    DROP COLUMN customer_id,
    ADD CONSTRAINT identity_proofing_request_match_checks_request_fkey
        FOREIGN KEY (organization_id, request_id)
            REFERENCES identity_proofing_requests (organization_id, id) ON DELETE CASCADE,
    ADD CONSTRAINT identity_proofing_request_match_checks_candidate_fkey
        FOREIGN KEY (organization_id, candidate_request_id)
            REFERENCES identity_proofing_requests (organization_id, id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE identity_proofing_request_match_checks
    DROP CONSTRAINT identity_proofing_request_match_checks_candidate_fkey,
    DROP CONSTRAINT identity_proofing_request_match_checks_request_fkey,
    ADD COLUMN customer_id UUID;

-- The requests' customer is restored by the migration that replaces it.
UPDATE identity_proofing_request_match_checks m
SET customer_id = (SELECT r.customer_id FROM identity_proofing_requests r WHERE r.id = m.request_id)
WHERE m.customer_id IS NULL;

ALTER TABLE identity_proofing_request_match_checks
    ALTER COLUMN customer_id SET NOT NULL,
    ADD FOREIGN KEY (organization_id, customer_id, request_id)
        REFERENCES identity_proofing_requests (organization_id, customer_id, id) ON DELETE CASCADE,
    ADD FOREIGN KEY (organization_id, customer_id, candidate_request_id)
        REFERENCES identity_proofing_requests (organization_id, customer_id, id) ON DELETE CASCADE;
