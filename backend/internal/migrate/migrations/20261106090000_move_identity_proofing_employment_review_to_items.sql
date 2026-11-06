-- +goose Up
-- The Verzekeringsbericht's review becomes its step's own 'employment' review
-- item: the session keeps the identity's outcome. A request the wallet held in
-- review for its statement (review_reason) goes back to approved, with a
-- pending item due its retention from now; review_reason stays as the item's
-- why. Its purge time is as setPurgeAt (proofing/request_store.go) stores it
-- for an approved request, which the org's legal retention now holds: its
-- constants inlined (proofedNameRetention 30 days, defaultDataRetentionDays 7).
WITH held AS (
    SELECT r.id, r.organization_id, k.external, st.legal_retention_months,
        COALESCE(r.cancelled_at, r.ips_session_ended_at, r.completed_at) AS ended_at,
        r.flow_kind = 'identity' AS identity_flow,
        COALESCE(
            make_interval(secs => r.retention_override_seconds),
            CASE WHEN k.external THEN make_interval(days => COALESCE(st.data_retention_days, 7)) END,
            interval '30 days') AS retention
    FROM identity_proofing_requests r
    CROSS JOIN LATERAL (SELECT r.subject_kind = 'external' AS external) k
    LEFT JOIN identity_proofing_settings st ON st.organization_id = r.organization_id
    WHERE r.status = 'needs_review' AND r.review_reason IS NOT NULL AND r.purged_at IS NULL
),
approved AS (
    UPDATE identity_proofing_requests r
    SET status = 'approved', updated_at = now(),
        purge_at = GREATEST(h.ended_at + h.retention,
            CASE WHEN h.external AND h.identity_flow THEN h.ended_at + make_interval(months => h.legal_retention_months) END)
    FROM held h
    WHERE r.id = h.id
)
INSERT INTO identity_proofing_review_items (request_id, organization_id, kind, state, due_at)
SELECT h.id, h.organization_id, 'employment', 'pending', now() + h.retention
FROM held h;

-- +goose Down
WITH dropped AS (
    DELETE FROM identity_proofing_review_items WHERE kind = 'employment'
    RETURNING request_id, state
)
UPDATE identity_proofing_requests r SET status = 'needs_review', updated_at = now()
FROM dropped i
WHERE i.request_id = r.id AND i.state = 'pending' AND r.status = 'approved';
