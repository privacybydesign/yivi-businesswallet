-- +goose Up
-- The DUO diploma extracts a request's subject uploaded and the wallet
-- accepted: signed by DUO, unchanged, and naming the person who proved their
-- identity. Only what is printed about the qualification is kept, never the
-- PDF, the holder's name or date of birth (matched, then dropped). Purging the
-- request deletes them.
CREATE TABLE identity_proofing_request_diplomas
(
    id              UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL,
    request_id      UUID        NOT NULL,
    document_type   TEXT        NOT NULL,
    qualification   TEXT        NOT NULL,
    profiles        TEXT[]      NOT NULL DEFAULT '{}',
    institution     TEXT        NOT NULL,
    place_of_issue  TEXT        NOT NULL,
    date_awarded    DATE        NOT NULL,
    nlqf_level      TEXT        NOT NULL DEFAULT '',
    eqf_level       TEXT        NOT NULL DEFAULT '',
    -- The number DUO prints, which duo.nl/diplomacontrole checks.
    document_number TEXT        NOT NULL,
    signed_at       TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (request_id, document_number),
    FOREIGN KEY (organization_id, request_id)
        REFERENCES identity_proofing_requests (organization_id, id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE identity_proofing_request_diplomas;
