-- +goose Up
-- An issued attestation that moves (claimed, declined, revoked) and an incoming
-- credential offer that arrives or is decided tell the org's attestation pages.
CREATE TRIGGER issued_attestations_event
AFTER INSERT OR UPDATE ON issued_attestations
FOR EACH ROW
EXECUTE FUNCTION notify_wallet_event('attestations');

CREATE TRIGGER credential_offers_event
AFTER INSERT OR UPDATE ON credential_offers
FOR EACH ROW
EXECUTE FUNCTION notify_wallet_event('attestations');

-- +goose Down
DROP TRIGGER credential_offers_event ON credential_offers;
DROP TRIGGER issued_attestations_event ON issued_attestations;
