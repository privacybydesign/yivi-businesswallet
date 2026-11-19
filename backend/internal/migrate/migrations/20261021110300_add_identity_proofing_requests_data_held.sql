-- +goose Up
-- What an erasure left because the org's legal retention still holds it: how
-- many of the person's sessions, and when the last of them can be erased. Set
-- with the decision, declined or approved, so the person is told what is kept
-- by law and until when once the request's own matches went with its purge.
ALTER TABLE identity_proofing_requests
    ADD COLUMN data_matches_held INTEGER,
    ADD COLUMN data_held_until   TIMESTAMPTZ,
    ADD CONSTRAINT identity_proofing_requests_data_held_check CHECK (
        (data_matches_held IS NULL) = (data_held_until IS NULL)
        AND (data_matches_held IS NULL OR (data_matches_held > 0 AND flow_kind = 'data_erasure')));

-- +goose Down
ALTER TABLE identity_proofing_requests
    DROP CONSTRAINT identity_proofing_requests_data_held_check,
    DROP COLUMN data_held_until,
    DROP COLUMN data_matches_held;
