-- +goose Up
-- Regula liveness transactions to delete at Regula once their proofing
-- session is over, by the session's tag: a retried, backed-off queue.
CREATE TABLE identity_proofing_regula_sweeps
(
    tag        TEXT PRIMARY KEY,
    due_at     TIMESTAMPTZ NOT NULL,
    attempts   INTEGER     NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX identity_proofing_regula_sweeps_due_idx
    ON identity_proofing_regula_sweeps (due_at);

-- +goose Down
DROP TABLE identity_proofing_regula_sweeps;
