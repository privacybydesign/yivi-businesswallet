-- +goose Up
-- The stub KVK's Token Status List entry of each EBWOID it issues. KVK revokes per
-- company, so an entry names the EUID, not a wallet: the org does not exist yet
-- when its EBWOID is issued.
CREATE TABLE ebwoid_status_entries
(
    status_index BIGINT      PRIMARY KEY CHECK (status_index >= 0),
    euid         TEXT        NOT NULL,
    revoked_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_ebwoid_status_entries_euid ON ebwoid_status_entries (euid);

-- +goose Down
DROP TABLE ebwoid_status_entries;
