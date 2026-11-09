-- +goose Up
-- 'always_review' is 'required' with every address or statement sent to a
-- reviewer, also one its check would pass, per step of a flow.
ALTER TABLE identity_proofing_flow_settings
    DROP CONSTRAINT identity_proofing_flow_settings_address_check,
    ADD CONSTRAINT identity_proofing_flow_settings_address_check CHECK (address IN ('off', 'required', 'always_review')),
    DROP CONSTRAINT identity_proofing_flow_settings_employment_check,
    ADD CONSTRAINT identity_proofing_flow_settings_employment_check CHECK (employment IN ('off', 'required', 'always_review'));

-- +goose Down
UPDATE identity_proofing_flow_settings SET address = 'required' WHERE address = 'always_review';
UPDATE identity_proofing_flow_settings SET employment = 'required' WHERE employment = 'always_review';
ALTER TABLE identity_proofing_flow_settings
    DROP CONSTRAINT identity_proofing_flow_settings_address_check,
    ADD CONSTRAINT identity_proofing_flow_settings_address_check CHECK (address IN ('off', 'required')),
    DROP CONSTRAINT identity_proofing_flow_settings_employment_check,
    ADD CONSTRAINT identity_proofing_flow_settings_employment_check CHECK (employment IN ('off', 'required'));
