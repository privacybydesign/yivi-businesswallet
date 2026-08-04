-- +goose Up
-- The Veramo issuer's credential uuid, captured when a credential is issued
-- (check-offer's `uuid`). It is the handle the issuer's revocation API keys on
-- (POST /{instance}/api/revoke-credential), so revoking an attestation can flip
-- the matching bit on the issuer's Token Status List. Empty until the recipient
-- claims the credential (and for rows issued before this column existed).
ALTER TABLE issued_attestations
    ADD COLUMN credential_uuid TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE issued_attestations
    DROP COLUMN credential_uuid;
