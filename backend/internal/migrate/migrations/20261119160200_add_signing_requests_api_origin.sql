-- +goose Up
-- A signing request may now be made by the organisation's own software, with one
-- of its API keys, instead of by a member in the wallet:
--   * created_by is NULL for such a request and api_key_id names the key (kept
--     NULL once that key is deleted);
--   * notify_signers is false when the caller asked for no mail, because it
--     hands each signee their signing link itself;
--   * return_url is where a signee's signing page sends them back to when they
--     are done; empty for none.
ALTER TABLE signing_requests ALTER COLUMN created_by DROP NOT NULL;

ALTER TABLE signing_requests
    ADD COLUMN api_key_id     UUID REFERENCES identity_proofing_api_keys (id) ON DELETE SET NULL,
    ADD COLUMN created_via    TEXT    NOT NULL DEFAULT 'member' CHECK (created_via IN ('member', 'api')),
    ADD COLUMN notify_signers BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN return_url     TEXT    NOT NULL DEFAULT '';

-- A member's request names the member; an API request names none.
ALTER TABLE signing_requests
    ADD CONSTRAINT signing_requests_origin CHECK (
        (created_via = 'member' AND api_key_id IS NULL)
            OR (created_via = 'api' AND created_by IS NULL));

-- +goose Down
-- An API request has no member to be created_by.
DELETE FROM signing_requests WHERE created_via = 'api';
-- A member's request whose creator has since been removed cannot exist: the
-- foreign key cascades. So every remaining row has a created_by.
ALTER TABLE signing_requests DROP CONSTRAINT signing_requests_origin;
ALTER TABLE signing_requests
    DROP COLUMN return_url,
    DROP COLUMN notify_signers,
    DROP COLUMN created_via,
    DROP COLUMN api_key_id;
ALTER TABLE signing_requests ALTER COLUMN created_by SET NOT NULL;
