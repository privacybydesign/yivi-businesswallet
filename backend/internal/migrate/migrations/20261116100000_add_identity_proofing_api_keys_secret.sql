-- +goose Up
-- The key itself, sealed with the proofing encryption key, so an admin can
-- read it again. NULL for a key made before, of which only the hash is kept.
ALTER TABLE identity_proofing_api_keys ADD COLUMN secret_ciphertext BYTEA;

-- +goose Down
ALTER TABLE identity_proofing_api_keys DROP COLUMN secret_ciphertext;
