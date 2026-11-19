-- +goose Up
-- A session moving in the proofing engine (the phone opened it, finished its
-- part) tells the verify pages of the org, which follow a session's progress.
CREATE TRIGGER identity_proofing_sessions_event
AFTER INSERT OR UPDATE ON identity_proofing_sessions
FOR EACH ROW
EXECUTE FUNCTION notify_wallet_event('proofing-progress', 'organization_id');

-- +goose Down
DROP TRIGGER identity_proofing_sessions_event ON identity_proofing_sessions;
