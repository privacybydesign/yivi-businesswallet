-- +goose Up
-- A target's trail (a proofing request's timeline, a held credential's
-- history) and the proofing purge's scrub read audit_events by target; without
-- this they filter every event of the org.
CREATE INDEX idx_audit_events_target ON audit_events (organization_id, target_type, target_id, occurred_at, id);

-- +goose Down
DROP INDEX idx_audit_events_target;
