-- +goose Up
-- 'registered' is 'required' with the registered (BRP) address required in
-- the Yivi disclosure too, instead of optional.
ALTER TABLE identity_proofing_flow_settings
    DROP CONSTRAINT identity_proofing_flow_settings_address_check,
    ADD CONSTRAINT identity_proofing_flow_settings_address_check CHECK (address IN ('off', 'required', 'registered'));
ALTER TABLE identity_proofing_requests
    DROP CONSTRAINT identity_proofing_requests_address_check,
    ADD CONSTRAINT identity_proofing_requests_address_check CHECK (address IN ('off', 'required', 'registered'));

-- +goose Down
UPDATE identity_proofing_flow_settings SET address = 'required' WHERE address = 'registered';
UPDATE identity_proofing_requests SET address = 'required' WHERE address = 'registered';
ALTER TABLE identity_proofing_flow_settings
    DROP CONSTRAINT identity_proofing_flow_settings_address_check,
    ADD CONSTRAINT identity_proofing_flow_settings_address_check CHECK (address IN ('off', 'required'));
ALTER TABLE identity_proofing_requests
    DROP CONSTRAINT identity_proofing_requests_address_check,
    ADD CONSTRAINT identity_proofing_requests_address_check CHECK (address IN ('off', 'required'));
