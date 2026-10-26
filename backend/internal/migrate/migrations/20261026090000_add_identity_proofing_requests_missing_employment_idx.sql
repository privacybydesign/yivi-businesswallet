-- +goose Up
-- RejectMissingEmployment and NextMissingEmployment find the approved requests
-- still owing a Verzekeringsbericht (missingEmploymentWhere), by approval time.
CREATE INDEX identity_proofing_requests_missing_employment_idx
    ON identity_proofing_requests (completed_at)
    WHERE employment = 'required' AND status = 'approved' AND employment_ciphertext IS NULL
        AND purged_at IS NULL AND cancelled_at IS NULL AND completed_at IS NOT NULL;

-- +goose Down
DROP INDEX identity_proofing_requests_missing_employment_idx;
