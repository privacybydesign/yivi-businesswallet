-- +goose Up
-- The Token Status List entry of each credential the EU reference issuer issues
-- (CS-10): the issuer reserves one per credential it signs, possibly several per
-- offer. An entry reads invalid once its issued_attestations row is revoked.
CREATE TABLE issuer_status_entries
(
    status_index          BIGINT      PRIMARY KEY CHECK (status_index >= 0),
    issued_attestation_id UUID        NOT NULL REFERENCES issued_attestations (id) ON DELETE CASCADE,
    expires_on            DATE,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_issuer_status_entries_issued_attestation_id ON issuer_status_entries (issued_attestation_id);
CREATE INDEX idx_issued_attestations_issuance_id ON issued_attestations (issuance_id);

-- +goose Down
DROP INDEX idx_issued_attestations_issuance_id;
DROP TABLE issuer_status_entries;
