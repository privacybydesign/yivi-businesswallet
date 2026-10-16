-- +goose Up
-- A provider session belongs to one request: RequestStore.GetBySession reads a
-- single row by it. The unique index replaces the plain one.
DROP INDEX identity_proofing_requests_ips_session_idx;
CREATE UNIQUE INDEX identity_proofing_requests_ips_session_idx
    ON identity_proofing_requests (ips_session_id) WHERE ips_session_id IS NOT NULL;

ALTER TABLE identity_proofing_requests
    -- A purged request holds none of its subject's personal data: what
    -- RequestStore.Purge clears stays cleared, whichever write comes after it.
    ADD CONSTRAINT identity_proofing_requests_purged_check CHECK (
        purged_at IS NULL OR (
            subject_name = '' AND subject_email = ''
            AND proofed_name_ciphertext IS NULL AND proofed_name_purge_after IS NULL
            AND expected_birth_date_ciphertext IS NULL
            AND reference_photo_ciphertext IS NULL AND reference_photo_mime IS NULL
            AND data_export_until IS NULL AND redirect_url IS NULL
            AND yivi_transaction_id IS NULL AND identity_match_key IS NULL)),
    -- The closed sets the table's comments name: proofingprovider.Method and
    -- the eIDAS levels. assurance_level is not one: it names a tier of the
    -- flow's own ladder.
    ADD CONSTRAINT identity_proofing_requests_method_check
        CHECK (method IN ('idem_app', 'yivi_app', 'browser')),
    ADD CONSTRAINT identity_proofing_requests_eidas_level_check
        CHECK (eidas_level IN ('low', 'substantial', 'high')),
    ADD CONSTRAINT identity_proofing_requests_required_assurance_level_check
        CHECK (required_assurance_level IN ('low', 'substantial', 'high'));

-- The engine's session.Status values.
ALTER TABLE identity_proofing_sessions
    ADD CONSTRAINT identity_proofing_sessions_status_check
        CHECK (status IN ('created', 'opened', 'in_progress', 'needs_review', 'approved', 'rejected', 'expired', 'cancelled'));

-- +goose Down
ALTER TABLE identity_proofing_sessions
    DROP CONSTRAINT identity_proofing_sessions_status_check;

ALTER TABLE identity_proofing_requests
    DROP CONSTRAINT identity_proofing_requests_required_assurance_level_check,
    DROP CONSTRAINT identity_proofing_requests_eidas_level_check,
    DROP CONSTRAINT identity_proofing_requests_method_check,
    DROP CONSTRAINT identity_proofing_requests_purged_check;

DROP INDEX identity_proofing_requests_ips_session_idx;
CREATE INDEX identity_proofing_requests_ips_session_idx
    ON identity_proofing_requests (ips_session_id);
