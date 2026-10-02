-- +goose Up
-- A request for one known person: the identity approved must be subject_name,
-- born on the sealed birth date, or the request is rejected. The birth date is
-- held only until the request is decided (or purged); expects_subject stays,
-- so the outcome still reads as a match.
ALTER TABLE identity_proofing_requests
    ADD COLUMN expects_subject BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN expected_birth_date_ciphertext BYTEA,
    ADD CONSTRAINT identity_proofing_requests_expected_birth_date_check
        CHECK (expected_birth_date_ciphertext IS NULL OR expects_subject);

-- +goose Down
ALTER TABLE identity_proofing_requests
    DROP CONSTRAINT identity_proofing_requests_expected_birth_date_check,
    DROP COLUMN expected_birth_date_ciphertext,
    DROP COLUMN expects_subject;
