-- +goose Up
-- Per-credential image (logo) for an attestation schema, mirroring the issuer
-- logo on org_issuer_settings. logo_bytes holds the raw image (NULL when unset);
-- logo_content_type is its MIME type (e.g. "image/png"). The API serves the bytes
-- at /attestations/schemas/{id}/logo for the admin preview, and the generated
-- issuer config bundle embeds them as a self-contained data: URI in the
-- per-credential display (the hosted issuer's wallet-facing metadata cannot reach
-- a business-wallet endpoint).
ALTER TABLE attestation_schemas
    ADD COLUMN logo_bytes        BYTEA,
    ADD COLUMN logo_content_type TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE attestation_schemas
    DROP COLUMN logo_bytes,
    DROP COLUMN logo_content_type;
