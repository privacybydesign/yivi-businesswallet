-- +goose Up
-- One member who must sign a given signing request. sign_order drives sequential
-- mode (ascending); it is ignored in parallel mode. status advances pending ->
-- signed as each member completes their own signing ceremony against the current
-- document, or -> failed if their ceremony errors.
CREATE TABLE signing_request_signers
(
    request_id UUID        NOT NULL REFERENCES signing_requests (id) ON DELETE CASCADE,
    user_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    sign_order INT         NOT NULL DEFAULT 0,
    status     TEXT        NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'signed', 'failed')),
    signed_at  TIMESTAMPTZ,
    PRIMARY KEY (request_id, user_id)
);

CREATE INDEX idx_signing_request_signers_request ON signing_request_signers (request_id);
-- Finds the requests awaiting a given member (their "to sign" list).
CREATE INDEX idx_signing_request_signers_user ON signing_request_signers (user_id);

-- +goose Down
DROP TABLE signing_request_signers;
