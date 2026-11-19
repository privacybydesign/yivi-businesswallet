-- +goose Up
-- The WE BUILD rulebook a schema was imported from (internal/attestation/rulebook),
-- or NULL for a schema the organization defined itself. A rulebook schema's vct and
-- attributes are fixed, and issued claims are validated against the rulebook.
ALTER TABLE attestation_schemas
    ADD COLUMN rulebook TEXT;

-- +goose Down
ALTER TABLE attestation_schemas
    DROP COLUMN rulebook;
