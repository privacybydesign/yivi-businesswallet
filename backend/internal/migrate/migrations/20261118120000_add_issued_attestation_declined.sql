-- +goose Up
-- An organization that receives an offer over QERDS and declines it tells the
-- issuer, which marks the offer 'declined'.
ALTER TABLE issued_attestations
    DROP CONSTRAINT issued_attestations_status_check,
    ADD CONSTRAINT issued_attestations_status_check
        CHECK (status IN ('offered', 'claimed', 'expired', 'revoked', 'cancelled', 'failed', 'declined'));

-- +goose Down
UPDATE issued_attestations SET status = 'cancelled' WHERE status = 'declined';

ALTER TABLE issued_attestations
    DROP CONSTRAINT issued_attestations_status_check,
    ADD CONSTRAINT issued_attestations_status_check
        CHECK (status IN ('offered', 'claimed', 'expired', 'revoked', 'cancelled', 'failed'));
