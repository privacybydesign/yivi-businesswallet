-- +goose Up
-- A member's request without a flow retention is purged, like a customer's,
-- 30 days (proofing.proofedNameRetention) after it settled. The store sets
-- purge_at on every write (proofing.refreshPurgeAt); this sets it once for the
-- unpurged member requests written before, by the same rule.
UPDATE identity_proofing_requests
SET purge_at = COALESCE(cancelled_at, ips_session_ended_at,
        CASE WHEN status IN ('approved', 'rejected') THEN completed_at END,
        CASE WHEN status IN ('pending', 'in_progress') THEN ips_session_expires_at END,
        CASE WHEN status = 'pending' AND ips_session_id IS NULL THEN link_expires_at END)
    + interval '30 days'
WHERE customer_id IS NULL AND retention_override_seconds IS NULL AND purged_at IS NULL;

-- +goose Down
UPDATE identity_proofing_requests
SET purge_at = NULL
WHERE customer_id IS NULL AND retention_override_seconds IS NULL AND purged_at IS NULL;
