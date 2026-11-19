-- +goose Up
-- A webhook delivery queued, sent or failed tells the developer page.
CREATE TRIGGER identity_proofing_webhook_deliveries_event
AFTER INSERT OR UPDATE ON identity_proofing_webhook_deliveries
FOR EACH ROW
EXECUTE FUNCTION notify_wallet_event('webhooks');

-- +goose Down
DROP TRIGGER identity_proofing_webhook_deliveries_event ON identity_proofing_webhook_deliveries;
