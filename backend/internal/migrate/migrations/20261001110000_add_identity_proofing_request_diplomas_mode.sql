-- +goose Up
-- The flow's diploma setting when the request was sent, as
-- required_assurance_level keeps its level: a later change to the flow does
-- not change what a sent request asks.
ALTER TABLE identity_proofing_requests
    ADD COLUMN diplomas TEXT NOT NULL DEFAULT 'off' CHECK (diplomas IN ('off', 'optional', 'required'));

-- +goose Down
ALTER TABLE identity_proofing_requests DROP COLUMN diplomas;
