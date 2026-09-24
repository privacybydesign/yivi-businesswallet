-- +goose Up
-- A pending signer may now refuse a request outright instead of only ever being
-- able to sign it: a new terminal per-signer status ('declined', alongside the
-- existing pending/signed/failed) with an optional free-text reason, captured at
-- decline time. Incremental PAdES cannot produce a document one selected signer
-- refused, so signing_requests.status gains the matching terminal 'declined'
-- value (no CHECK constraint to extend there — see 20260807114100).
ALTER TABLE signing_request_signers
    DROP CONSTRAINT signing_request_signers_status_check,
    ADD CONSTRAINT signing_request_signers_status_check
        CHECK (status IN ('pending', 'signed', 'failed', 'declined')),
    ADD COLUMN declined_at    TIMESTAMPTZ,
    ADD COLUMN decline_reason TEXT NOT NULL DEFAULT '';

-- +goose Down
-- A declined signer cannot survive the narrower check the old constraint enforced.
UPDATE signing_request_signers SET status = 'failed' WHERE status = 'declined';

ALTER TABLE signing_request_signers
    DROP CONSTRAINT signing_request_signers_status_check,
    DROP COLUMN declined_at,
    DROP COLUMN decline_reason,
    ADD CONSTRAINT signing_request_signers_status_check
        CHECK (status IN ('pending', 'signed', 'failed'));
