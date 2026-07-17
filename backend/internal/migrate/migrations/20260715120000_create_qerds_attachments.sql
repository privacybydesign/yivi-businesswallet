-- +goose Up
-- Message payloads (eIDAS Art 43-44 / ETSI EN 319 522). The store is
-- content-opaque: the payload may be large and E2E-encrypted ciphertext we
-- cannot read, so the row carries only integrity metadata (content_hash,
-- size_bytes) alongside the bytes. See .ai/features/qerds.md §4.
--
-- Blob-column MVP: bytes live in `content`. `storage_ref` is reserved for a
-- later object-storage backend (bytes elsewhere, referenced by this column);
-- exactly one of the two carries the payload, which keeps the swap a code
-- change behind the store interface rather than a schema break.
CREATE TABLE qerds_attachments
(
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id   UUID        NOT NULL REFERENCES qerds_messages (id) ON DELETE CASCADE,
    filename     TEXT        NOT NULL,
    content_type TEXT        NOT NULL,
    content_hash TEXT        NOT NULL,
    size_bytes   BIGINT      NOT NULL,
    content      BYTEA,
    storage_ref  TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Exactly one of content / storage_ref carries the payload: the blob-column
    -- MVP fills content, a later object-storage backend fills storage_ref. The
    -- CHECK keeps that swap a code change behind the store interface, not a
    -- schema break (no DROP NOT NULL on content).
    CONSTRAINT qerds_attachments_payload_present
        CHECK ((content IS NOT NULL) <> (storage_ref IS NOT NULL))
);

CREATE INDEX idx_qerds_attachments_message_id ON qerds_attachments (message_id);

-- +goose Down
DROP TABLE qerds_attachments;
