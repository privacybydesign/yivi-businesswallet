-- +goose Up
-- The lifetime the request's engine session was created with, as the engine
-- gave it: a hosted page that ran out says the check was not finished within
-- it. Today's settings may differ by then, and ips_session_expires_at moves
-- when the engine extends a session, so neither tells it. NULL before a
-- session, and for one created before this column.
ALTER TABLE identity_proofing_requests
    ADD COLUMN ips_session_ttl_seconds INTEGER CHECK (ips_session_ttl_seconds > 0);

-- +goose Down
ALTER TABLE identity_proofing_requests
    DROP COLUMN ips_session_ttl_seconds;
