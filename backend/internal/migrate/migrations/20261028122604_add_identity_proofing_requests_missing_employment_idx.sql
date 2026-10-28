-- +goose Up
-- RejectMissingEmployment and NextMissingEmployment find the approved requests
-- still owing a Verzekeringsbericht (missingEmploymentWhere), by when the
-- identity check finished.
CREATE INDEX identity_proofing_requests_missing_employment_idx
    ON identity_proofing_requests (checked_at)
    WHERE employment = 'required' AND status = 'approved' AND employment_ciphertext IS NULL
        AND purged_at IS NULL AND cancelled_at IS NULL AND checked_at IS NOT NULL;

-- +goose Down
DROP INDEX identity_proofing_requests_missing_employment_idx;
