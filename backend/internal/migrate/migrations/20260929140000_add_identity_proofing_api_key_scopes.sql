-- +goose Up
-- What a customer key may do; existing and new keys get every scope.
ALTER TABLE identity_proofing_api_keys
    ADD COLUMN scopes TEXT[] NOT NULL DEFAULT ARRAY['sessions:write', 'sessions:read', 'results:read', 'flows:read'];

-- +goose Down
ALTER TABLE identity_proofing_api_keys DROP COLUMN scopes;
