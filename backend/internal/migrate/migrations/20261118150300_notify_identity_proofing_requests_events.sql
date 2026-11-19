-- +goose Up
-- Requests tell the org's pages through the wallet's event stream now: a new
-- one too, which the old trigger missed, and each one's own followers (the
-- verify page) on any change.
DROP TRIGGER identity_proofing_requests_notify_reviews ON identity_proofing_requests;
DROP FUNCTION notify_identity_proofing_reviews();

CREATE TRIGGER identity_proofing_requests_event_created
AFTER INSERT ON identity_proofing_requests
FOR EACH ROW
EXECUTE FUNCTION notify_wallet_event('reviews');

CREATE TRIGGER identity_proofing_requests_event_reviews
AFTER UPDATE ON identity_proofing_requests
FOR EACH ROW
WHEN (
    OLD.status IS DISTINCT FROM NEW.status
    OR OLD.purged_at IS DISTINCT FROM NEW.purged_at
    OR OLD.ips_session_ended_at IS DISTINCT FROM NEW.ips_session_ended_at
)
EXECUTE FUNCTION notify_wallet_event('reviews');

CREATE TRIGGER identity_proofing_requests_event_request
AFTER UPDATE ON identity_proofing_requests
FOR EACH ROW
EXECUTE FUNCTION notify_wallet_event('proofing-request', 'id');

-- Any step of a request moving, for the org's on-screen verify page.
CREATE TRIGGER identity_proofing_requests_event_progress
AFTER UPDATE ON identity_proofing_requests
FOR EACH ROW
EXECUTE FUNCTION notify_wallet_event('proofing-progress');

-- +goose Down
DROP TRIGGER identity_proofing_requests_event_progress ON identity_proofing_requests;
DROP TRIGGER identity_proofing_requests_event_request ON identity_proofing_requests;
DROP TRIGGER identity_proofing_requests_event_reviews ON identity_proofing_requests;
DROP TRIGGER identity_proofing_requests_event_created ON identity_proofing_requests;

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
    OR OLD.purged_at IS DISTINCT FROM NEW.purged_at
    OR OLD.ips_session_ended_at IS DISTINCT FROM NEW.ips_session_ended_at
)
EXECUTE FUNCTION notify_identity_proofing_reviews();
