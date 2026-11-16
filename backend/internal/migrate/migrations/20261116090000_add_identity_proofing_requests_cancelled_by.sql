-- +goose Up
-- Who cancelled a request: the org's API key, a member, or the person
-- declining on the hosted page. NULL for one cancelled before this was kept.
ALTER TABLE identity_proofing_requests
    ADD COLUMN cancelled_by TEXT CHECK (cancelled_by IN ('api_key', 'member', 'subject'));

-- +goose Down
ALTER TABLE identity_proofing_requests DROP COLUMN cancelled_by;
