-- +goose Up
-- When a request is purged, stored so the hourly purge reads an index instead
-- of computing it for every unpurged row (it joined the customer and called
-- now() per row). The store keeps it: every write to one of its inputs sets it
-- in the same transaction (proofing.refreshPurgeAt, which holds the rule), and
-- a customer's changed retention sets it again for its requests.
ALTER TABLE identity_proofing_requests ADD COLUMN purge_at TIMESTAMPTZ;

-- A one-off backfill of the rows already there, by the rule as it stands
-- (proofing.setPurgeAt): their retention after they settled, or a data
-- request's review lapse 30 days after it went there.
UPDATE identity_proofing_requests r SET purge_at = LEAST(
        COALESCE(r.cancelled_at, r.ips_session_ended_at,
                 CASE WHEN r.status IN ('approved', 'rejected') THEN r.completed_at END,
                 CASE WHEN r.status IN ('pending', 'in_progress') THEN r.ips_session_expires_at END,
                 CASE WHEN r.status = 'pending' AND r.ips_session_id IS NULL THEN r.link_expires_at END)
            + COALESCE(make_interval(secs => r.retention_override_seconds),
                       make_interval(days => (SELECT c.data_retention_days FROM identity_proofing_customers c
                                               WHERE c.id = r.customer_id))),
        CASE WHEN r.status = 'needs_review' AND r.flow_kind IN ('data_access', 'data_erasure')
             THEN r.completed_at + make_interval(days => 30) END);

-- +goose Down
ALTER TABLE identity_proofing_requests DROP COLUMN purge_at;
