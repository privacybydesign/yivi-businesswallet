-- +goose Up
-- IF NOT EXISTS: a dev database that ran an earlier version of
-- 20261002120200 has this table from there.
-- A data request's matches: the customer's sessions whose proofed identity is
-- the subject's, strong when proven with the same document, probable on name
-- and date of birth alone. approved is the reviewer's choice per match, NULL
-- until the decision. A child of identity_proofing_requests on both sides.
CREATE TABLE IF NOT EXISTS identity_proofing_request_matches
(
    request_id         UUID NOT NULL REFERENCES identity_proofing_requests (id) ON DELETE CASCADE,
    matched_request_id UUID NOT NULL REFERENCES identity_proofing_requests (id) ON DELETE CASCADE,
    level              TEXT NOT NULL CHECK (level IN ('strong', 'probable')),
    approved           BOOLEAN,
    PRIMARY KEY (request_id, matched_request_id),
    CHECK (request_id <> matched_request_id)
);

CREATE INDEX IF NOT EXISTS identity_proofing_request_matches_matched_idx
    ON identity_proofing_request_matches (matched_request_id);

-- +goose Down
DROP TABLE IF EXISTS identity_proofing_request_matches;
