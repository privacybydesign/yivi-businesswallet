-- +goose Up
-- A session moving in the proofing engine (the phone opened it, finished its
-- part) tells the org's verify pages, and the page that follows the one
-- request it belongs to. The session row has no request id, so its own
-- function looks it up: identity_proofing_requests.ips_session_id is unique.
-- +goose StatementBegin
CREATE FUNCTION notify_proofing_session_event() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('wallet_events', 'proofing-progress:' || NEW.organization_id);
    PERFORM pg_notify('wallet_events', 'proofing-request:' || r.id)
    FROM identity_proofing_requests r WHERE r.ips_session_id = NEW.id;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER identity_proofing_sessions_event
AFTER INSERT OR UPDATE ON identity_proofing_sessions
FOR EACH ROW
EXECUTE FUNCTION notify_proofing_session_event();

-- +goose Down
DROP TRIGGER identity_proofing_sessions_event ON identity_proofing_sessions;
DROP FUNCTION notify_proofing_session_event();
