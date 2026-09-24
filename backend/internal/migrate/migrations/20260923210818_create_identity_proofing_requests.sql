-- +goose Up
-- identity_proofing_requests is one e-mailed ask for a person to prove their
-- identity against one of the org's IPS flows. Any member may send one; the
-- subject need not be a member, so the subject is a name and address, not a
-- membership.
--
-- The e-mail carries the wallet link (token_hash, valid until link_expires_at),
-- never an IPS session: IPS caps a session at 15 minutes and its claim QR at 5,
-- far shorter than a mail sits in an inbox. The IPS session is created when the
-- recipient opens the link and presses start, and replaced if it lapses while
-- the link is still valid. ips_session_token_ciphertext is that session's
-- relying-party bearer token, sealed like the org's API key.
--
-- Only the outcome is kept: status, the achieved assurance levels and IPS's
-- error code. Never the document fields, BSN or images IPS returns.
--
-- status: pending (no live IPS session), in_progress (a live IPS session),
-- then approved / rejected / needs_review. needs_review is not final - IPS may
-- still decide it - so it keeps being reconciled. "expired" is derived from
-- link_expires_at, not stored.
CREATE TABLE identity_proofing_requests
(
    id                           UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    organization_id              UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    requested_by                 UUID        REFERENCES users (id) ON DELETE SET NULL,
    subject_name                 TEXT        NOT NULL,
    subject_email                TEXT        NOT NULL,
    flow_id                      TEXT        NOT NULL,
    flow_name                    TEXT        NOT NULL,
    token_hash                   BYTEA       NOT NULL UNIQUE,
    link_expires_at              TIMESTAMPTZ NOT NULL,
    status                       TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'in_progress', 'approved', 'rejected', 'needs_review')),
    ips_session_id               TEXT,
    ips_session_token_ciphertext BYTEA,
    ips_session_expires_at       TIMESTAMPTZ,
    assurance_level              TEXT,
    eidas_level                  TEXT,
    error_code                   TEXT,
    created_at                   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                   TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at                 TIMESTAMPTZ,
    CHECK ((ips_session_id IS NULL) = (ips_session_token_ciphertext IS NULL))
);

CREATE INDEX identity_proofing_requests_org_created_idx
    ON identity_proofing_requests (organization_id, created_at DESC);
CREATE INDEX identity_proofing_requests_org_requester_idx
    ON identity_proofing_requests (organization_id, requested_by, created_at DESC);

-- +goose Down
DROP TABLE identity_proofing_requests;
