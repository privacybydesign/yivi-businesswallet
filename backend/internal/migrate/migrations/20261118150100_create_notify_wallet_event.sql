-- +goose Up
-- The one trigger function behind the wallet's event streams (internal/events):
-- it NOTIFYs 'wallet_events' with '<kind>:<id>', where the trigger names the
-- kind and the column that holds the id. A page following that topic re-reads
-- then, so nothing polls for the change.
-- +goose StatementBegin
CREATE FUNCTION notify_wallet_event() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    changed jsonb;
BEGIN
    IF TG_OP = 'DELETE' THEN
        changed := to_jsonb(OLD);
    ELSE
        changed := to_jsonb(NEW);
    END IF;
    PERFORM pg_notify('wallet_events', TG_ARGV[0] || ':' || (changed ->> TG_ARGV[1]));
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION notify_wallet_event();
