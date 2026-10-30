-- +goose Up
-- Tells the wallet, with the org's id, that what its reviewers see may have
-- changed: a request went into or out of review, its address review moved, or
-- it was purged. Every writer is covered, so no page has to poll for it. The
-- channel is proofing.ReviewsChannel.
-- +goose StatementBegin
CREATE FUNCTION notify_identity_proofing_reviews() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('identity_proofing_reviews', NEW.organization_id::text);
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

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

-- +goose Down
DROP TRIGGER identity_proofing_requests_notify_reviews ON identity_proofing_requests;
DROP FUNCTION notify_identity_proofing_reviews();
