-- +goose Up
-- An Idempotency-Key is kept per API key, not per org: two keys of one org that
-- send the same Idempotency-Key each run their own call, and neither is
-- replayed the other's answer, which carries the subject's name, e-mail
-- address and hosted link. The kept answers name no key and are a 24-hour
-- cache, so they are dropped: a retry across this migration runs again. A
-- key's answers go with it.
TRUNCATE identity_proofing_idempotency_keys;

ALTER TABLE identity_proofing_idempotency_keys
    DROP CONSTRAINT identity_proofing_idempotency_keys_pkey,
    ADD COLUMN api_key_id UUID NOT NULL,
    ADD PRIMARY KEY (api_key_id, key),
    ADD CONSTRAINT identity_proofing_idempotency_keys_api_key_fkey
        FOREIGN KEY (organization_id, api_key_id)
            REFERENCES identity_proofing_api_keys (organization_id, id) ON DELETE CASCADE;

-- +goose Down
TRUNCATE identity_proofing_idempotency_keys;

ALTER TABLE identity_proofing_idempotency_keys
    DROP CONSTRAINT identity_proofing_idempotency_keys_api_key_fkey,
    DROP CONSTRAINT identity_proofing_idempotency_keys_pkey,
    DROP COLUMN api_key_id,
    ADD PRIMARY KEY (organization_id, key);
