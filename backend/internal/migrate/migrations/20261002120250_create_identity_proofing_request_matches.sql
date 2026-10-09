-- +goose Up
-- A data request's matches: the customer's sessions that may be the subject's.
-- strong when proven with the same document, probable on name and date of
-- birth alone, email or name for an unfinished session sent to the same
-- address or typed with the same name (it holds no proofed identity).
-- approved is the reviewer's choice per match, NULL until the decision. A
-- child of identity_proofing_requests on both sides, within one org.
CREATE TABLE identity_proofing_request_matches
(
    organization_id    UUID NOT NULL,
    request_id         UUID NOT NULL,
    matched_request_id UUID NOT NULL,
    level              TEXT NOT NULL CHECK (level IN ('strong', 'probable', 'email', 'name')),
    approved           BOOLEAN,
    PRIMARY KEY (request_id, matched_request_id),
    CHECK (request_id <> matched_request_id),
    FOREIGN KEY (organization_id, request_id)
        REFERENCES identity_proofing_requests (organization_id, id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id, matched_request_id)
        REFERENCES identity_proofing_requests (organization_id, id) ON DELETE CASCADE
);

CREATE INDEX identity_proofing_request_matches_matched_idx
    ON identity_proofing_request_matches (matched_request_id);

-- +goose Down
DROP TABLE identity_proofing_request_matches;
