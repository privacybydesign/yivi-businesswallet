-- +goose Up
-- 'registered' is gone: every flow that asks the address now requires the
-- registered (BRP) address in the Yivi disclosure, which is what it meant.
-- Its flows and requests become 'required'.
-- +goose StatementBegin
DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['identity_proofing_flow_settings', 'identity_proofing_requests'] LOOP
        EXECUTE format(
            'UPDATE %1$I SET address = ''required'' WHERE address = ''registered''; ALTER TABLE %1$I DROP CONSTRAINT %2$I, ADD CONSTRAINT %2$I CHECK (address IN (''off'', ''required''))',
            t, t || '_address_check');
    END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['identity_proofing_flow_settings', 'identity_proofing_requests'] LOOP
        EXECUTE format(
            'ALTER TABLE %1$I DROP CONSTRAINT %2$I, ADD CONSTRAINT %2$I CHECK (address IN (''off'', ''required'', ''registered''))',
            t, t || '_address_check');
    END LOOP;
END $$;
-- +goose StatementEnd
