-- +goose Up
-- Whether the request asks its subject for their postal address once the
-- identity is approved (the flow's setting at send), and the address they
-- entered: sealed, and cleared when the request is purged.
ALTER TABLE identity_proofing_requests
    ADD COLUMN address            TEXT NOT NULL DEFAULT 'off' CHECK (address IN ('off', 'required')),
    ADD COLUMN address_ciphertext BYTEA,
    ADD COLUMN address_added_at   TIMESTAMPTZ;

-- +goose Down
ALTER TABLE identity_proofing_requests
    DROP COLUMN address,
    DROP COLUMN address_ciphertext,
    DROP COLUMN address_added_at;
