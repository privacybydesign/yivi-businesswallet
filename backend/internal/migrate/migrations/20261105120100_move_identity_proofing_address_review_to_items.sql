-- +goose Up
-- An address review becomes an 'address' review item, due its request's data
-- retention after the address was entered (30 days for a member's request,
-- as proofedNameRetention).
INSERT INTO identity_proofing_review_items
    (request_id, organization_id, kind, state, created_at, due_at, decided_by, decided_at)
SELECT r.id, r.organization_id, 'address', r.address_review,
    COALESCE(r.address_added_at, r.updated_at),
    COALESCE(r.address_added_at, r.updated_at) + COALESCE(
        make_interval(secs => r.retention_override_seconds),
        CASE WHEN r.subject_kind = 'external' THEN make_interval(days => COALESCE(st.data_retention_days, 7)) END,
        interval '30 days'),
    r.address_reviewed_by,
    CASE WHEN r.address_review <> 'pending' THEN COALESCE(r.address_reviewed_at, r.updated_at) END
FROM identity_proofing_requests r
LEFT JOIN identity_proofing_settings st ON st.organization_id = r.organization_id
WHERE r.address_review IS NOT NULL;

-- The review items notify for themselves now.
DROP TRIGGER identity_proofing_requests_notify_reviews ON identity_proofing_requests;
CREATE TRIGGER identity_proofing_requests_notify_reviews
AFTER UPDATE ON identity_proofing_requests
FOR EACH ROW
WHEN (
    OLD.status IS DISTINCT FROM NEW.status
    OR OLD.purged_at IS DISTINCT FROM NEW.purged_at
    OR OLD.ips_session_ended_at IS DISTINCT FROM NEW.ips_session_ended_at
)
EXECUTE FUNCTION notify_identity_proofing_reviews();

ALTER TABLE identity_proofing_requests
    DROP COLUMN address_reviewed_at,
    DROP COLUMN address_reviewed_by,
    DROP COLUMN address_review;

-- +goose Down
ALTER TABLE identity_proofing_requests
    ADD COLUMN address_review TEXT CHECK (address_review IN ('pending', 'confirmed', 'rejected')),
    ADD COLUMN address_reviewed_by UUID REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN address_reviewed_at TIMESTAMPTZ;

-- A lapsed review has no column value: it reads as none.
WITH moved AS (
    DELETE FROM identity_proofing_review_items WHERE kind = 'address'
    RETURNING request_id, state, decided_by, decided_at
)
UPDATE identity_proofing_requests r
SET address_review = i.state, address_reviewed_by = i.decided_by, address_reviewed_at = i.decided_at
FROM moved i
WHERE i.request_id = r.id AND i.state <> 'lapsed';

DROP TRIGGER identity_proofing_requests_notify_reviews ON identity_proofing_requests;
CREATE TRIGGER identity_proofing_requests_notify_reviews
AFTER UPDATE ON identity_proofing_requests
FOR EACH ROW
WHEN (
    OLD.status IS DISTINCT FROM NEW.status
    OR OLD.address_review IS DISTINCT FROM NEW.address_review
    OR OLD.purged_at IS DISTINCT FROM NEW.purged_at
    OR OLD.ips_session_ended_at IS DISTINCT FROM NEW.ips_session_ended_at
)
EXECUTE FUNCTION notify_identity_proofing_reviews();
