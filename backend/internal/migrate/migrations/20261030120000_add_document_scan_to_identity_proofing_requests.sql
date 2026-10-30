-- +goose Up
-- A flow with document_photo has it taken in the browser by a document scan
-- service: document_scan is whether the request asks for it (its flow's steps
-- at send), document_scan_transaction_id the scan service's transaction the
-- page started (it reads the photos, so a purge clears it), and
-- document_scanned_at when its photos reached the engine.
ALTER TABLE identity_proofing_requests
    ADD COLUMN document_scan BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN document_scan_transaction_id TEXT,
    ADD COLUMN document_scanned_at TIMESTAMPTZ;

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
            AND address_ciphertext IS NULL AND employment_ciphertext IS NULL
            AND registered_address_ciphertext IS NULL
            AND document_scan_transaction_id IS NULL));

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
            AND address_ciphertext IS NULL AND employment_ciphertext IS NULL
            AND registered_address_ciphertext IS NULL));

ALTER TABLE identity_proofing_requests
    DROP COLUMN document_scanned_at,
    DROP COLUMN document_scan_transaction_id,
    DROP COLUMN document_scan;
