-- +goose Up
-- A signer signing, declining or failing tells the same pages as its request.
-- The signer row has no organisation, so its own function looks it up.
-- +goose StatementBegin
CREATE FUNCTION notify_signing_signer_event() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('wallet_events', 'signing-request:' || NEW.request_id);
    PERFORM pg_notify('wallet_events', 'signing:' || r.organization_id)
    FROM signing_requests r WHERE r.id = NEW.request_id;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER signing_request_signers_event
AFTER UPDATE ON signing_request_signers
FOR EACH ROW
EXECUTE FUNCTION notify_signing_signer_event();

-- +goose Down
DROP TRIGGER signing_request_signers_event ON signing_request_signers;
DROP FUNCTION notify_signing_signer_event();
