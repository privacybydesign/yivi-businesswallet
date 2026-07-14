-- +goose Up
-- Append-only ERDS evidence (eIDAS Art 44). Rows are inserted, never updated:
-- the evidence is what gives a message its legal effect, so it is immutable.
CREATE TABLE qerds_evidence
(
    id                  UUID        PRIMARY KEY DEFAULT uuidv7(),
    message_id          UUID        NOT NULL REFERENCES qerds_messages (id) ON DELETE CASCADE,
    evidence_type       TEXT        NOT NULL,
    provider_ref        TEXT        NOT NULL,
    qualified_timestamp TIMESTAMPTZ NOT NULL,
    raw_evidence        BYTEA       NOT NULL,
    verified_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_qerds_evidence_message_id ON qerds_evidence (message_id);

-- +goose Down
DROP TABLE qerds_evidence;
