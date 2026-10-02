-- +goose Up
-- A non-user actor (a customer API key, `api_key:<prefix>`) names itself here;
-- actor_user_id stays for members.
ALTER TABLE audit_events ADD COLUMN actor_label TEXT;

-- +goose Down
ALTER TABLE audit_events DROP COLUMN actor_label;
