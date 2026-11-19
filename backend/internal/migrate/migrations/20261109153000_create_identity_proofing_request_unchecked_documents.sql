-- +goose Up
-- A diploma extract or Verzekeringsbericht whose seal could not be checked at
-- upload, as the trust lists or the revocation responder were unreachable.
-- While it waits (ciphertext set): the whole PDF and the identity it must
-- name, sealed, for a reviewer to check again. kind is the review item it
-- waits under. Once its check decided, or its item was decided or lapsed,
-- the PDF goes and outcome says what became of it, for the reviewer: kept, a
-- refusal reason, or not_checked. The request's purge deletes the rows.
CREATE TABLE identity_proofing_request_unchecked_documents
(
    id              UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL,
    request_id      UUID        NOT NULL,
    kind            TEXT        NOT NULL,
    file_name       TEXT        NOT NULL,
    -- The same file uploaded twice is kept once.
    file_sha256     BYTEA       NOT NULL,
    ciphertext      BYTEA,
    outcome         TEXT,
    checked_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT identity_proofing_request_unchecked_documents_kind_check
        CHECK (kind IN ('diplomas', 'employment')),
    CONSTRAINT identity_proofing_request_unchecked_documents_waiting_check
        CHECK ((ciphertext IS NULL) = (outcome IS NOT NULL)),
    CONSTRAINT identity_proofing_request_unchecked_documents_file_key
        UNIQUE (request_id, file_sha256),
    FOREIGN KEY (organization_id, request_id)
        REFERENCES identity_proofing_requests (organization_id, id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE identity_proofing_request_unchecked_documents;
