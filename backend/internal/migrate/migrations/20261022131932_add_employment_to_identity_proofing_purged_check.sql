-- +goose Up
-- A purged request holds no employment either: RequestStore.Purge clears it.
ALTER TABLE identity_proofing_requests
    DROP CONSTRAINT identity_proofing_requests_purged_check,
    ADD CONSTRAINT identity_proofing_requests_purged_check CHECK (
        purged_at IS NULL OR (
            subject_name = '' AND subject_email = ''
            AND proofed_name_ciphertext IS NULL AND proofed_name_purge_after IS NULL
            AND expected_birth_date_ciphertext IS NULL
            AND reference_photo_ciphertext IS NULL AND reference_photo_mime IS NULL
            AND data_export_until IS NULL AND redirect_url IS NULL
            AND yivi_transaction_id IS NULL AND identity_match_key IS NULL
            AND address_ciphertext IS NULL AND employment_ciphertext IS NULL));

-- +goose Down
ALTER TABLE identity_proofing_requests
    DROP CONSTRAINT identity_proofing_requests_purged_check,
    ADD CONSTRAINT identity_proofing_requests_purged_check CHECK (
        purged_at IS NULL OR (
            subject_name = '' AND subject_email = ''
            AND proofed_name_ciphertext IS NULL AND proofed_name_purge_after IS NULL
            AND expected_birth_date_ciphertext IS NULL
            AND reference_photo_ciphertext IS NULL AND reference_photo_mime IS NULL
            AND data_export_until IS NULL AND redirect_url IS NULL
            AND yivi_transaction_id IS NULL AND identity_match_key IS NULL
            AND address_ciphertext IS NULL));
