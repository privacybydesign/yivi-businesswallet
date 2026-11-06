-- +goose Up
-- The Verzekeringsbericht's review becomes its step's own 'employment' review
-- item: the session keeps the identity's outcome. A request the wallet held in
-- review for its statement (review_reason) goes back to approved, with a
-- pending item due its retention from now; review_reason stays as the item's
-- why.
INSERT INTO identity_proofing_review_items (request_id, organization_id, kind, state, due_at)
SELECT r.id, r.organization_id, 'employment', 'pending',
    now() + COALESCE(
        make_interval(secs => r.retention_override_seconds),
        CASE WHEN r.subject_kind = 'external' THEN make_interval(days => COALESCE(st.data_retention_days, 7)) END,
        interval '30 days')
FROM identity_proofing_requests r
LEFT JOIN identity_proofing_settings st ON st.organization_id = r.organization_id
WHERE r.status = 'needs_review' AND r.review_reason IS NOT NULL AND r.purged_at IS NULL;

UPDATE identity_proofing_requests
SET status = 'approved', updated_at = now()
WHERE status = 'needs_review' AND review_reason IS NOT NULL AND purged_at IS NULL;

-- Their purge time as setPurgeAt (proofing/request_store.go) stores it for an
-- approved request, which the org's legal retention now holds: its constants
-- inlined (proofedNameRetention 30 days, defaultDataRetentionDays 7).
UPDATE identity_proofing_requests r SET purge_at = GREATEST(
        COALESCE(r.cancelled_at, r.ips_session_ended_at, r.completed_at)
            + COALESCE(make_interval(secs => r.retention_override_seconds),
                CASE WHEN r.subject_kind = 'external' THEN make_interval(days => COALESCE((
                    SELECT st.data_retention_days FROM identity_proofing_settings st
                    WHERE st.organization_id = r.organization_id), 7)) END,
                interval '30 days'),
        CASE WHEN r.subject_kind = 'external' AND r.flow_kind = 'identity'
            THEN COALESCE(r.cancelled_at, r.ips_session_ended_at, r.completed_at)
                + (SELECT make_interval(months => st.legal_retention_months) FROM identity_proofing_settings st
                    WHERE st.organization_id = r.organization_id) END)
WHERE r.status = 'approved' AND r.review_reason IS NOT NULL AND r.purged_at IS NULL;

-- +goose Down
UPDATE identity_proofing_requests r SET status = 'needs_review', updated_at = now()
FROM identity_proofing_review_items i
WHERE i.request_id = r.id AND i.kind = 'employment' AND i.state = 'pending' AND r.status = 'approved';

DELETE FROM identity_proofing_review_items WHERE kind = 'employment';
