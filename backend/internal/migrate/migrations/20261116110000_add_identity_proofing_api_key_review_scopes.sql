-- +goose Up
-- Every key has every scope: the org's existing keys get the review ones too.
-- +goose StatementBegin
DO $$
DECLARE
    base  CONSTANT TEXT[] := ARRAY ['sessions:write', 'sessions:read', 'results:read', 'flows:read',
        'flows:write', 'settings:read', 'settings:write', 'webhooks:write'];
    added CONSTANT TEXT[] := ARRAY ['reviews:read', 'reviews:write'];
BEGIN
    UPDATE identity_proofing_api_keys
    SET scopes = scopes || ARRAY(
        SELECT s FROM unnest(added) AS s
        WHERE NOT s = ANY (scopes))
    WHERE NOT scopes @> added;

    EXECUTE format('ALTER TABLE identity_proofing_api_keys ALTER COLUMN scopes SET DEFAULT %L', base || added);
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE
    base  CONSTANT TEXT[] := ARRAY ['sessions:write', 'sessions:read', 'results:read', 'flows:read',
        'flows:write', 'settings:read', 'settings:write', 'webhooks:write'];
    added CONSTANT TEXT[] := ARRAY ['reviews:read', 'reviews:write'];
BEGIN
    EXECUTE format('ALTER TABLE identity_proofing_api_keys ALTER COLUMN scopes SET DEFAULT %L', base);

    UPDATE identity_proofing_api_keys
    SET scopes = ARRAY(
        SELECT s FROM unnest(scopes) AS s
        WHERE NOT s = ANY (added))
    WHERE scopes && added;
END
$$;
-- +goose StatementEnd
