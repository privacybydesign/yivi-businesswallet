-- +goose Up
-- Split the single "revoke" withdrawal into two terminal states. An unclaimed
-- offer (status 'offered') that is withdrawn is now 'cancelled' — nothing was
-- ever held, so it is not a revocation. 'revoked' is reserved for withdrawing an
-- already-claimed credential (the IETF Token Status List-backed path). cancelled_at
-- mirrors revoked_at so each event carries its own timestamp.
ALTER TABLE issued_attestations
    DROP CONSTRAINT issued_attestations_status_check,
    ADD CONSTRAINT issued_attestations_status_check
        CHECK (status IN ('offered', 'claimed', 'expired', 'revoked', 'cancelled', 'failed'));

ALTER TABLE issued_attestations
    ADD COLUMN cancelled_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE issued_attestations
    DROP COLUMN cancelled_at;

ALTER TABLE issued_attestations
    DROP CONSTRAINT issued_attestations_status_check,
    ADD CONSTRAINT issued_attestations_status_check
        CHECK (status IN ('offered', 'claimed', 'expired', 'revoked', 'failed'));
