-- +goose Up
-- Every session event of a customer is a delivery, also without an endpoint of
-- its own: endpoint_url is the customer's endpoint it goes to, NULL for the
-- wallet's own default endpoint.
ALTER TABLE identity_proofing_webhook_deliveries
    ADD COLUMN endpoint_url TEXT;

UPDATE identity_proofing_webhook_deliveries d
SET endpoint_url = w.url
FROM identity_proofing_webhooks w
WHERE w.customer_id = d.customer_id;

-- +goose Down
ALTER TABLE identity_proofing_webhook_deliveries
    DROP COLUMN endpoint_url;
