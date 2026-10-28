-- +goose Up
-- A data request's matches stay requests of one org, which is all that kept
-- them within one customer now. Dropping the column drops the composite
-- foreign keys.
ALTER TABLE identity_proofing_request_matches
    DROP COLUMN customer_id,
    ADD CONSTRAINT identity_proofing_request_matches_request_fkey
        FOREIGN KEY (organization_id, request_id)
            REFERENCES identity_proofing_requests (organization_id, id) ON DELETE CASCADE,
    ADD CONSTRAINT identity_proofing_request_matches_matched_fkey
        FOREIGN KEY (organization_id, matched_request_id)
            REFERENCES identity_proofing_requests (organization_id, id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE identity_proofing_request_matches
    DROP CONSTRAINT identity_proofing_request_matches_matched_fkey,
    DROP CONSTRAINT identity_proofing_request_matches_request_fkey,
    ADD COLUMN customer_id UUID;

-- The requests' customer is restored by the migration that replaces it.
UPDATE identity_proofing_request_matches m
SET customer_id = (SELECT r.customer_id FROM identity_proofing_requests r WHERE r.id = m.request_id)
WHERE m.customer_id IS NULL;

ALTER TABLE identity_proofing_request_matches
    ALTER COLUMN customer_id SET NOT NULL,
    ADD FOREIGN KEY (organization_id, customer_id, request_id)
        REFERENCES identity_proofing_requests (organization_id, customer_id, id) ON DELETE CASCADE,
    ADD FOREIGN KEY (organization_id, customer_id, matched_request_id)
        REFERENCES identity_proofing_requests (organization_id, customer_id, id) ON DELETE CASCADE;
