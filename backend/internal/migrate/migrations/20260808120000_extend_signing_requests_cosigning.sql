-- +goose Up
-- Expand a signing request from a single-user, sign-immediately action into a
-- co-signing object: it is created by one member, carries the original uploaded
-- PDF, is signed by one or more members (tracked in signing_request_signers), and
-- once fully signed is delivered to a recipient over a chosen channel. The signed
-- PAdES accumulates on signed_document, re-written after each signer's pass.
ALTER TABLE signing_requests RENAME COLUMN user_id TO created_by;

ALTER TABLE signing_requests
    ADD COLUMN original_document BYTEA,
    ADD COLUMN signing_mode      TEXT        NOT NULL DEFAULT 'parallel'
        CHECK (signing_mode IN ('parallel', 'sequential')),
    ADD COLUMN recipient_channel TEXT        NOT NULL DEFAULT 'none'
        CHECK (recipient_channel IN ('none', 'qerds', 'email')),
    ADD COLUMN recipient_address TEXT        NOT NULL DEFAULT '',
    ADD COLUMN recipient_name    TEXT        NOT NULL DEFAULT '',
    ADD COLUMN message           TEXT        NOT NULL DEFAULT '',
    ADD COLUMN delivery_status   TEXT        NOT NULL DEFAULT 'not_requested'
        CHECK (delivery_status IN ('not_requested', 'pending', 'delivered', 'failed')),
    ADD COLUMN delivery_error    TEXT        NOT NULL DEFAULT '';

-- credential_id used to be the single signer's credential; co-signing tracks the
-- credential per signer as each one signs, so the request no longer sets it.
-- Default it so inserts that omit it still satisfy the NOT NULL.
ALTER TABLE signing_requests ALTER COLUMN credential_id SET DEFAULT '';

-- Cursor pagination for the org-wide signed-documents history (newest first).
CREATE INDEX idx_signing_requests_org_created ON signing_requests (organization_id, created_at DESC, id);

-- +goose Down
DROP INDEX idx_signing_requests_org_created;

ALTER TABLE signing_requests ALTER COLUMN credential_id DROP DEFAULT;

ALTER TABLE signing_requests
    DROP COLUMN original_document,
    DROP COLUMN signing_mode,
    DROP COLUMN recipient_channel,
    DROP COLUMN recipient_address,
    DROP COLUMN recipient_name,
    DROP COLUMN message,
    DROP COLUMN delivery_status,
    DROP COLUMN delivery_error;

ALTER TABLE signing_requests RENAME COLUMN created_by TO user_id;
