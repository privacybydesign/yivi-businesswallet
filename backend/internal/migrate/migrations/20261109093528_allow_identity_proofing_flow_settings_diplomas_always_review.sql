-- +goose Up
-- 'always_review' is 'required' with the kept diploma extracts sent to a
-- reviewer, as the address and the Verzekeringsbericht have it.
-- format names the constraint once.
-- +goose StatementBegin
DO $$
BEGIN
    EXECUTE format(
        'ALTER TABLE identity_proofing_flow_settings DROP CONSTRAINT %1$I, ADD CONSTRAINT %1$I CHECK (diplomas IN (''off'', ''required'', ''always_review''))',
        'identity_proofing_flow_settings_diplomas_check');
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    EXECUTE format(
        'UPDATE identity_proofing_flow_settings SET diplomas = ''required'' WHERE diplomas = ''always_review''; ALTER TABLE identity_proofing_flow_settings DROP CONSTRAINT %1$I, ADD CONSTRAINT %1$I CHECK (diplomas IN (''off'', ''required''))',
        'identity_proofing_flow_settings_diplomas_check');
END $$;
-- +goose StatementEnd
