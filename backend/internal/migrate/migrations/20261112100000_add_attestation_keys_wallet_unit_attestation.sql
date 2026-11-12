-- +goose Up
-- The wallet unit attestation covering a wallet_managed key: the unit whose
-- BWIA and SKA attest the device that holds it. NULL for qualified_certificate
-- keys, which a QTSP holds.
ALTER TABLE attestation_keys ADD COLUMN wallet_unit_attestation JSONB;

-- +goose Down
ALTER TABLE attestation_keys DROP COLUMN wallet_unit_attestation;
