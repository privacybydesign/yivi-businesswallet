-- +goose Up
-- The page a flow's hosted links send their subject back to when the request
-- names none of its own: on one of the org's redirect origins, empty for none.
-- A flow that always ends on its own thank-you page has none.
ALTER TABLE identity_proofing_flow_settings
    ADD COLUMN hosted_return_url TEXT NOT NULL DEFAULT '';
ALTER TABLE identity_proofing_flow_settings
    ADD CONSTRAINT identity_proofing_flow_settings_hosted_return_url_check
        CHECK (hosted_completion = 'redirect' OR char_length(hosted_return_url) = 0);

-- +goose Down
ALTER TABLE identity_proofing_flow_settings
    DROP CONSTRAINT identity_proofing_flow_settings_hosted_return_url_check;
ALTER TABLE identity_proofing_flow_settings
    DROP COLUMN hosted_return_url;
