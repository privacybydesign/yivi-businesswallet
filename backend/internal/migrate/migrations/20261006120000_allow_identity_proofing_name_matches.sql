-- +goose Up
-- A data request also matches the customer's unfinished sessions whose typed
-- name is the requester's: they hold that name and no proofed identity.
ALTER TABLE identity_proofing_request_matches
    DROP CONSTRAINT identity_proofing_request_matches_level_check;
ALTER TABLE identity_proofing_request_matches
    ADD CONSTRAINT identity_proofing_request_matches_level_check
        CHECK (level IN ('strong', 'probable', 'email', 'name'));

-- +goose Down
DELETE FROM identity_proofing_request_matches WHERE level = 'name';
ALTER TABLE identity_proofing_request_matches
    DROP CONSTRAINT identity_proofing_request_matches_level_check;
ALTER TABLE identity_proofing_request_matches
    ADD CONSTRAINT identity_proofing_request_matches_level_check
        CHECK (level IN ('strong', 'probable', 'email'));
