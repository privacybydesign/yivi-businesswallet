-- +goose Up
-- The diploma step is part of a flow or not: when it is, it must be done.
-- "optional" is dropped; a flow or request set to it now requires diplomas.
UPDATE identity_proofing_flow_diploma_settings SET diplomas = 'required' WHERE diplomas = 'optional';
ALTER TABLE identity_proofing_flow_diploma_settings
    DROP CONSTRAINT identity_proofing_flow_diploma_settings_diplomas_check,
    ADD CONSTRAINT identity_proofing_flow_diploma_settings_diplomas_check CHECK (diplomas IN ('off', 'required'));
UPDATE identity_proofing_requests SET diplomas = 'required' WHERE diplomas = 'optional';
ALTER TABLE identity_proofing_requests
    DROP CONSTRAINT identity_proofing_requests_diplomas_check,
    ADD CONSTRAINT identity_proofing_requests_diplomas_check CHECK (diplomas IN ('off', 'required'));

-- +goose Down
ALTER TABLE identity_proofing_requests
    DROP CONSTRAINT identity_proofing_requests_diplomas_check,
    ADD CONSTRAINT identity_proofing_requests_diplomas_check CHECK (diplomas IN ('off', 'optional', 'required'));
ALTER TABLE identity_proofing_flow_diploma_settings
    DROP CONSTRAINT identity_proofing_flow_diploma_settings_diplomas_check,
    ADD CONSTRAINT identity_proofing_flow_diploma_settings_diplomas_check CHECK (diplomas IN ('off', 'optional', 'required'));
