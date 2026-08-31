-- +goose Up
-- Inbound OpenID4VCI credential offers delivered over QERDS, held until a human
-- decides. Receiving an offer used to redeem it straight into the org's holder
-- engine; a credential now enters the wallet only once an admin accepts, so this
-- table is the queue between "the message arrived" and "the wallet holds it"
-- (see .ai/features/oid4vci-over-qerds.md §7).
--
-- credential_offer is the openid-credential-offer:// deeplink from the message
-- body. It is a bearer token: whoever redeems it gets the credential, so it is
-- never served to the frontend, only replayed server-side on accept.
CREATE TABLE credential_offers
(
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    -- the inbound QERDS message that carried the offer; also the idempotency key,
    -- since a re-delivered message resolves to the same row (evidence chain intact).
    source_message_id UUID        NOT NULL REFERENCES qerds_messages (id) ON DELETE CASCADE,
    -- who offered it, as the envelope and the transport tell it: sender_org_name is
    -- the display name from the body, sender_address the originalSender the sending
    -- side wrote, from_party the AS4 party the gateway authenticated ('' when the
    -- provider exposes none).
    sender_org_name   TEXT        NOT NULL,
    sender_address    TEXT        NOT NULL,
    from_party        TEXT        NOT NULL,
    credential_name   TEXT        NOT NULL,
    credential_offer  TEXT        NOT NULL,
    -- 'accepting' is the claim an admin's accept takes before the redemption runs:
    -- the pending -> accepting transition is a single guarded UPDATE, so of two
    -- concurrent accepts only one reaches the issuer and the loser sees no
    -- pending offer. It settles as 'accepted', or back to 'pending' when the
    -- redemption produced nothing (see Service.AcceptOffer).
    status            TEXT        NOT NULL CHECK (status IN ('pending', 'accepting', 'accepted', 'declined')),
    -- when the offer was settled for good; NULL while it waits and while an
    -- acceptance is in flight. The acting admin is in the audit trail
    -- (attestation.offer_accepted / offer_declined), not here.
    decided_at        TIMESTAMPTZ,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, source_message_id)
);

CREATE INDEX idx_credential_offers_pending ON credential_offers (organization_id, received_at DESC)
    WHERE status = 'pending';

-- +goose Down
DROP TABLE credential_offers;
