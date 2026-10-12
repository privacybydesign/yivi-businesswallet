-- +goose Up
-- What a data request found when it read one candidate session's identity
-- from the engine: level is the match (strong or probable), NULL when it is
-- not the person or the engine no longer holds it. Kept so a retried search
-- reads each candidate once. A child of identity_proofing_requests on both
-- sides, within one org.
CREATE TABLE identity_proofing_request_match_checks
(
    organization_id      UUID NOT NULL,
    request_id           UUID NOT NULL,
    candidate_request_id UUID NOT NULL,
    level                TEXT CHECK (level IN ('strong', 'probable')),
    PRIMARY KEY (request_id, candidate_request_id),
    CHECK (request_id <> candidate_request_id),
    FOREIGN KEY (organization_id, request_id)
        REFERENCES identity_proofing_requests (organization_id, id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id, candidate_request_id)
        REFERENCES identity_proofing_requests (organization_id, id) ON DELETE CASCADE
);

CREATE INDEX identity_proofing_request_match_checks_candidate_idx
    ON identity_proofing_request_match_checks (candidate_request_id);

-- +goose Down
DROP TABLE identity_proofing_request_match_checks;
