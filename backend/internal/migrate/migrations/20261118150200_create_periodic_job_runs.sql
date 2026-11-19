-- +goose Up
-- When each recurring background job last started, so of all replicas one runs
-- it per interval and a restart does not reset its schedule (database.Periodic).
CREATE TABLE periodic_job_runs (
    name        TEXT        PRIMARY KEY,
    last_run_at TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE periodic_job_runs;
