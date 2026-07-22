-- +goose Up
-- Enrich the interim address book with structured organisation data so issuing an
-- attestation to an external organisation can copy that org's real details (legal
-- name / KVK number / EUID) into the credential. All optional: contacts saved before
-- this migration, and QERDS recipients that are not organisations, simply have none.
ALTER TABLE qerds_contacts
    ADD COLUMN legal_name TEXT,
    ADD COLUMN kvk_number TEXT,
    ADD COLUMN euid       TEXT;

-- +goose Down
ALTER TABLE qerds_contacts
    DROP COLUMN legal_name,
    DROP COLUMN kvk_number,
    DROP COLUMN euid;
