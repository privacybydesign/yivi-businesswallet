-- +goose Up
-- The deadline job ends hosted links that lapse unstarted: found by this
-- partial index, across every org.
CREATE INDEX identity_proofing_requests_open_link_idx
    ON identity_proofing_requests (link_expires_at)
    WHERE link_token_hash IS NOT NULL AND ips_session_id IS NULL
        AND ips_session_ended_at IS NULL AND status = 'pending';

-- +goose Down
DROP INDEX identity_proofing_requests_open_link_idx;
