-- +goose Up
-- A data request also matches the customer's unfinished sessions sent to the
-- same e-mail address: they hold no proofed identity to match on.
ALTER TABLE identity_proofing_request_matches
    DROP CONSTRAINT identity_proofing_request_matches_level_check;
ALTER TABLE identity_proofing_request_matches
    ADD CONSTRAINT identity_proofing_request_matches_level_check
        CHECK (level IN ('strong', 'probable', 'email'));

-- +goose Down
DELETE FROM identity_proofing_request_matches WHERE level = 'email';
ALTER TABLE identity_proofing_request_matches
    DROP CONSTRAINT identity_proofing_request_matches_level_check;
ALTER TABLE identity_proofing_request_matches
    ADD CONSTRAINT identity_proofing_request_matches_level_check
        CHECK (level IN ('strong', 'probable'));
