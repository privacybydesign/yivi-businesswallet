-- +goose Up
-- required_assurance_level is the eIDAS level the request's flow demanded when
-- it was sent (low, substantial, high). IPS approves on its checks alone, so
-- the wallet rejects an approval below it. NULL demands none, as do the rows
-- sent before this column.
ALTER TABLE identity_proofing_requests
    ADD COLUMN required_assurance_level TEXT;

-- +goose Down
ALTER TABLE identity_proofing_requests
    DROP COLUMN required_assurance_level;
