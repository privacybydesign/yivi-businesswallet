-- +goose Up
-- A signer of a co-signing request may now be an internal org member or an
-- external signee (someone outside the organisation, identified by name +
-- e-mail). Both sign with their own EUDI wallet through the same QTSP ceremony;
-- what differs is how they are identified and how they get in. So both signing
-- tables lose their "a signer is a row in users" assumption:
--   * signing_request_signers gets a surrogate key and a nullable user_id, plus
--     the external signee's name/e-mail and the hash of the one-time invitation
--     token that is their way in (no membership, no session).
--   * signing_credentials is re-keyed the same way, so an external signee's
--     linked QTSP credential can be cached per (org, e-mail).
ALTER TABLE signing_request_signers
    ADD COLUMN id                UUID        NOT NULL DEFAULT gen_random_uuid(),
    ADD COLUMN external_email    TEXT        NOT NULL DEFAULT '',
    ADD COLUMN external_name     TEXT        NOT NULL DEFAULT '',
    -- Only the hash is stored; the raw token exists once, in the invitation mail.
    -- NULL until the signee is invited: in sequential mode a later signee has no
    -- usable link until their turn comes.
    ADD COLUMN invite_token_hash BYTEA,
    ADD COLUMN invite_expires_at TIMESTAMPTZ;

-- The primary key goes first: Postgres refuses to drop NOT NULL from a column that
-- is still part of one.
ALTER TABLE signing_request_signers DROP CONSTRAINT signing_request_signers_pkey;
ALTER TABLE signing_request_signers ADD PRIMARY KEY (id);
ALTER TABLE signing_request_signers ALTER COLUMN user_id DROP NOT NULL;

-- A signer is exactly one of the two kinds: an internal member carries a user_id
-- and no external identity, an external signee the reverse.
ALTER TABLE signing_request_signers
    ADD CONSTRAINT signing_request_signers_identity CHECK (
        (user_id IS NOT NULL AND external_email = '')
            OR (user_id IS NULL AND external_email <> ''));

-- One row per member and per external address on a request (what the composite
-- primary key used to guarantee for members alone).
CREATE UNIQUE INDEX idx_signing_request_signers_member
    ON signing_request_signers (request_id, user_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX idx_signing_request_signers_external
    ON signing_request_signers (request_id, lower(external_email)) WHERE user_id IS NULL;
-- The invitation token is the external signee's only key, so it is looked up by
-- hash and must name at most one signer.
CREATE UNIQUE INDEX idx_signing_request_signers_token
    ON signing_request_signers (invite_token_hash) WHERE invite_token_hash IS NOT NULL;

ALTER TABLE signing_credentials
    ADD COLUMN id             UUID NOT NULL DEFAULT gen_random_uuid(),
    ADD COLUMN external_email TEXT NOT NULL DEFAULT '';

ALTER TABLE signing_credentials DROP CONSTRAINT signing_credentials_pkey;
ALTER TABLE signing_credentials ADD PRIMARY KEY (id);
ALTER TABLE signing_credentials ALTER COLUMN user_id DROP NOT NULL;

ALTER TABLE signing_credentials
    ADD CONSTRAINT signing_credentials_subject CHECK (
        (user_id IS NOT NULL AND external_email = '')
            OR (user_id IS NULL AND external_email <> ''));

CREATE UNIQUE INDEX idx_signing_credentials_member
    ON signing_credentials (organization_id, user_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX idx_signing_credentials_external
    ON signing_credentials (organization_id, lower(external_email)) WHERE user_id IS NULL;

-- +goose Down
-- External rows cannot survive a user_id that is NOT NULL again.
DELETE FROM signing_credentials WHERE user_id IS NULL;
DELETE FROM signing_request_signers WHERE user_id IS NULL;

DROP INDEX idx_signing_credentials_external;
DROP INDEX idx_signing_credentials_member;
ALTER TABLE signing_credentials DROP CONSTRAINT signing_credentials_subject;
ALTER TABLE signing_credentials DROP CONSTRAINT signing_credentials_pkey;
ALTER TABLE signing_credentials ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE signing_credentials ADD PRIMARY KEY (organization_id, user_id);
ALTER TABLE signing_credentials
    DROP COLUMN external_email,
    DROP COLUMN id;

DROP INDEX idx_signing_request_signers_token;
DROP INDEX idx_signing_request_signers_external;
DROP INDEX idx_signing_request_signers_member;
ALTER TABLE signing_request_signers DROP CONSTRAINT signing_request_signers_identity;
ALTER TABLE signing_request_signers DROP CONSTRAINT signing_request_signers_pkey;
ALTER TABLE signing_request_signers ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE signing_request_signers ADD PRIMARY KEY (request_id, user_id);
ALTER TABLE signing_request_signers
    DROP COLUMN invite_expires_at,
    DROP COLUMN invite_token_hash,
    DROP COLUMN external_name,
    DROP COLUMN external_email,
    DROP COLUMN id;
