-- +goose Up
-- The deadline job leases the due requests it re-checks, so API replicas never
-- ask IPS about the same session at once, and finds them by a partial index.
ALTER TABLE identity_proofing_requests
    ADD COLUMN ips_reconcile_leased_until TIMESTAMPTZ;
CREATE INDEX identity_proofing_requests_live_deadline_idx
    ON identity_proofing_requests (ips_session_expires_at)
    WHERE ips_session_id IS NOT NULL AND ips_session_ended_at IS NULL
        AND status IN ('pending', 'in_progress');

-- +goose Down
DROP INDEX identity_proofing_requests_live_deadline_idx;
ALTER TABLE identity_proofing_requests
    DROP COLUMN ips_reconcile_leased_until;
