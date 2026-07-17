-- +goose Up
-- Per-language credential display metadata for the "Schemas" tab: the SD-JWT VC
-- type metadata `display` array ([{lang,name}]) a wallet uses to show the
-- credential in the holder's language. Per-attribute labels live inside the
-- existing attributes JSONB, so only the credential-level array needs a column.
ALTER TABLE attestation_schemas
    ADD COLUMN display JSONB NOT NULL DEFAULT '[]'::jsonb;

-- +goose Down
ALTER TABLE attestation_schemas
    DROP COLUMN display;
