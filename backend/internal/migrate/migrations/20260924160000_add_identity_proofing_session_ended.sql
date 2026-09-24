-- +goose Up
-- A mailed proofing link now outlives any one IPS session: a session that ends
-- without an outcome (expired or cancelled at IPS) no longer ends the link, and
-- the recipient's next start restarts it. ips_session_ended_at is when the
-- wallet saw the attached session end undecided; NULL while it may still run or
-- decide. link_expires_at keeps the send-time expiry.
ALTER TABLE identity_proofing_requests
    ADD COLUMN ips_session_ended_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE identity_proofing_requests
    DROP COLUMN ips_session_ended_at;
