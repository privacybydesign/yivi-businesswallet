-- +goose Up
-- 'always_review' is 'required' with the kept diploma extracts sent to a
-- reviewer, as the address and the Verzekeringsbericht have it.
ALTER TABLE identity_proofing_requests
    DROP CONSTRAINT identity_proofing_requests_diplomas_check,
    ADD CONSTRAINT identity_proofing_requests_diplomas_check CHECK (diplomas IN ('off', 'required', 'always_review'));

-- +goose Down
UPDATE identity_proofing_requests SET diplomas = 'required' WHERE diplomas = 'always_review';
ALTER TABLE identity_proofing_requests
    DROP CONSTRAINT identity_proofing_requests_diplomas_check,
    ADD CONSTRAINT identity_proofing_requests_diplomas_check CHECK (diplomas IN ('off', 'required'));
