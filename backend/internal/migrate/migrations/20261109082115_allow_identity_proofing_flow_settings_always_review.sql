-- +goose Up
-- 'always_review' is 'required' with every address or statement sent to a
-- reviewer, also one its check would pass, per step of a flow.
-- One loop over both steps' columns, so each mode is written once.
-- +goose StatementBegin
DO $$
DECLARE
    c text;
BEGIN
    FOREACH c IN ARRAY ARRAY['address', 'employment'] LOOP
        EXECUTE format(
            'ALTER TABLE identity_proofing_flow_settings DROP CONSTRAINT %1$I, ADD CONSTRAINT %1$I CHECK (%2$I IN (''off'', ''required'', ''always_review''))',
            'identity_proofing_flow_settings_' || c || '_check', c);
    END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE
    c text;
BEGIN
    FOREACH c IN ARRAY ARRAY['address', 'employment'] LOOP
        EXECUTE format(
            'UPDATE identity_proofing_flow_settings SET %2$I = ''required'' WHERE %2$I = ''always_review''; ALTER TABLE identity_proofing_flow_settings DROP CONSTRAINT %1$I, ADD CONSTRAINT %1$I CHECK (%2$I IN (''off'', ''required''))',
            'identity_proofing_flow_settings_' || c || '_check', c);
    END LOOP;
END $$;
-- +goose StatementEnd
