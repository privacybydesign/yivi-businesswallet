-- +goose Up
-- The proofing mail now carries the IPS session itself (its vcmrtd deep link),
-- created at send, instead of a wallet link to a page where the recipient
-- pressed start. Nothing looks a request up by a link token any more, so the
-- token goes. link_expires_at stays: it is the mailed session's expiry.
ALTER TABLE identity_proofing_requests
    DROP COLUMN token_hash;

-- +goose Down
-- A dropped token cannot be restored; the column comes back empty and nullable.
ALTER TABLE identity_proofing_requests
    ADD COLUMN token_hash BYTEA UNIQUE;
