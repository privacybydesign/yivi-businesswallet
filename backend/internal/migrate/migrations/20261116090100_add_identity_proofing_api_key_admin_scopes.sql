-- +goose Up
-- Every key has every scope: the org's existing keys get the new ones too, so
-- they also set up flows, settings and the webhook.
UPDATE identity_proofing_api_keys
SET scopes = scopes || ARRAY(
    SELECT s FROM unnest(ARRAY ['flows:write', 'settings:read', 'settings:write', 'webhooks:write']) AS s
    WHERE NOT s = ANY (scopes));

ALTER TABLE identity_proofing_api_keys
    ALTER COLUMN scopes SET DEFAULT ARRAY ['sessions:write', 'sessions:read', 'results:read', 'flows:read',
        'flows:write', 'settings:read', 'settings:write', 'webhooks:write'];

-- +goose Down
ALTER TABLE identity_proofing_api_keys
    ALTER COLUMN scopes SET DEFAULT ARRAY ['sessions:write', 'sessions:read', 'results:read', 'flows:read'];

UPDATE identity_proofing_api_keys
SET scopes = ARRAY(
    SELECT s FROM unnest(scopes) AS s
    WHERE NOT s = ANY (ARRAY ['flows:write', 'settings:read', 'settings:write', 'webhooks:write']));
