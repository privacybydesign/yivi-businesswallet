-- +goose Up
-- A QERDS message received, sent or moved on tells the org's inbox.
CREATE TRIGGER qerds_messages_event
AFTER INSERT OR UPDATE ON qerds_messages
FOR EACH ROW
EXECUTE FUNCTION notify_wallet_event('qerds');

-- +goose Down
DROP TRIGGER qerds_messages_event ON qerds_messages;
