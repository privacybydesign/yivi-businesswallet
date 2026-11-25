-- +goose Up
-- A held credential that arrives, is removed or changes status tells the org's
-- attestation pages; a status change touches the row (RecordHeldStatusChange).
CREATE TRIGGER held_attestations_event
AFTER INSERT OR UPDATE ON held_attestations
FOR EACH ROW
EXECUTE FUNCTION notify_wallet_event('attestations');

-- The EU issuer reserves a status list entry while it signs a credential a
-- wallet collected, so a new entry is when the issuing org's offer is claimed.
-- The entry has no organization_id of its own: it is its issued attestation's.
-- +goose StatementBegin
CREATE FUNCTION notify_issuer_status_entry_event() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('wallet_events', 'attestations:' || a.organization_id)
    FROM issued_attestations a
    WHERE a.id = NEW.issued_attestation_id;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER issuer_status_entries_event
AFTER INSERT ON issuer_status_entries
FOR EACH ROW
EXECUTE FUNCTION notify_issuer_status_entry_event();

-- +goose Down
DROP TRIGGER issuer_status_entries_event ON issuer_status_entries;
DROP FUNCTION notify_issuer_status_entry_event();
DROP TRIGGER held_attestations_event ON held_attestations;
