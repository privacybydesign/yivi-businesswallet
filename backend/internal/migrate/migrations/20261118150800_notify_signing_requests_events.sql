-- +goose Up
-- A signing request created or moved on tells the org's signing pages, and
-- the external signee's page that follows this one request.
CREATE TRIGGER signing_requests_event
AFTER INSERT OR UPDATE ON signing_requests
FOR EACH ROW
EXECUTE FUNCTION notify_wallet_event('signing');

CREATE TRIGGER signing_requests_event_request
AFTER UPDATE ON signing_requests
FOR EACH ROW
EXECUTE FUNCTION notify_wallet_event('signing-request', 'id');

-- +goose Down
DROP TRIGGER signing_requests_event_request ON signing_requests;
DROP TRIGGER signing_requests_event ON signing_requests;
