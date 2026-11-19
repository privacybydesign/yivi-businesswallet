-- +goose Up
-- Whether the request asks its subject for their UWV Verzekeringsbericht once
-- the identity is approved (the flow's setting at send), and the employment it
-- listed: sealed, and cleared when the request is purged.
ALTER TABLE identity_proofing_requests
    ADD COLUMN employment            TEXT NOT NULL DEFAULT 'off' CHECK (employment IN ('off', 'required')),
    ADD COLUMN employment_ciphertext BYTEA,
    ADD COLUMN employment_added_at   TIMESTAMPTZ;

-- +goose Down
ALTER TABLE identity_proofing_requests
    DROP COLUMN employment,
    DROP COLUMN employment_ciphertext,
    DROP COLUMN employment_added_at;
