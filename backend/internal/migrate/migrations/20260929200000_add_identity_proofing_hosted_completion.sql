-- +goose Up
-- Where a hosted page hands its subject back: a customer lists the origins it
-- may redirect to and embed the page on, and a hosted request carries the
-- redirect it was created with and the language it opens in.
ALTER TABLE identity_proofing_customers
    ADD COLUMN allowed_redirect_origins TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE identity_proofing_requests
    ADD COLUMN redirect_url TEXT,
    ADD COLUMN language TEXT;

-- +goose Down
ALTER TABLE identity_proofing_requests
    DROP COLUMN language,
    DROP COLUMN redirect_url;

ALTER TABLE identity_proofing_customers
    DROP COLUMN allowed_redirect_origins;
