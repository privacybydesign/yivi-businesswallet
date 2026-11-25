-- +goose Up
-- An offer whose code the issuer refuses (used or expired) closes as 'failed'.
ALTER TABLE credential_offers
    DROP CONSTRAINT credential_offers_status_check,
    ADD CONSTRAINT credential_offers_status_check
        CHECK (status IN ('pending', 'accepting', 'accepted', 'declined', 'failed')); -- NOSONAR status values are SQL literals; a CHECK cannot use constants

-- +goose Down
UPDATE credential_offers SET status = 'declined' WHERE status = 'failed';

ALTER TABLE credential_offers
    DROP CONSTRAINT credential_offers_status_check,
    ADD CONSTRAINT credential_offers_status_check
        CHECK (status IN ('pending', 'accepting', 'accepted', 'declined'));
