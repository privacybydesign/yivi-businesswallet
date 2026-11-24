-- +goose Up
-- Why the issuer revoked the credential, told to the holder (WE BUILD CS-10 ISS-RV-09).
ALTER TABLE issued_attestations ADD COLUMN revocation_reason TEXT;

-- +goose Down
ALTER TABLE issued_attestations DROP COLUMN revocation_reason;
