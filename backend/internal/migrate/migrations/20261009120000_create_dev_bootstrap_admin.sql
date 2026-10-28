-- +goose Up
-- dev_bootstrap_admin is a local dev stack's (DEV_MODE) first Yivi login: the
-- user it created, a platform admin from then on. One row at most, so two
-- first logins at once cannot both claim it. Never written outside DEV_MODE.
CREATE TABLE dev_bootstrap_admin
(
    singleton  BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    user_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE dev_bootstrap_admin;
