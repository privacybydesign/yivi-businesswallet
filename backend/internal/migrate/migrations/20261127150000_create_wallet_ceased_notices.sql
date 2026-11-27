-- +goose Up
-- The orgs whose wallet was revoked because their activity ended and whose admins
-- still have to be told, within 24 hours (Annex 6(2)). Queued with the revocation,
-- retried until sent; warned_at is when we were alerted it was still unsent.
CREATE TABLE wallet_ceased_notices
(
    organization_id UUID        PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    queued_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    warned_at       TIMESTAMPTZ
);

-- +goose Down
DROP TABLE wallet_ceased_notices;
