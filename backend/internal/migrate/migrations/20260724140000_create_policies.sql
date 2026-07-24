-- +goose Up
-- Consent & approval policies (consent-approval-layer.md, #113). An admin-authored
-- rule (mode 3) that auto-approves or auto-declines a class of inbound approval
-- requests. Policies are evaluated first-match-wins by priority when a request is
-- enqueued; no match leaves the request pending for a human. A policy is itself a
-- revocable, audited authorisation, so it carries its authoring admin, a validity
-- window (reusing the wallet_representations / grant valid_from/valid_until shape)
-- and a revoked_at.
CREATE TABLE policies
(
    id                   UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    organization_id      UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    kind                 TEXT        NOT NULL CHECK (kind IN ('presentation', 'issuance')),
    -- Selector: counterparty (verifier / issuer) match. '*' matches any; a single
    -- trailing '*' is a prefix match; otherwise an exact identity (see policy.go).
    counterparty_pattern TEXT        NOT NULL CHECK (counterparty_pattern <> ''),
    -- Selector: the request's requested attribute set must contain all of these.
    -- Empty matches any attribute set.
    required_attributes  TEXT[]      NOT NULL DEFAULT '{}',
    effect               TEXT        NOT NULL CHECK (effect IN ('auto_approve', 'auto_decline')),
    -- For auto_approve, the subset of requested attributes to approve; empty means
    -- the full requested set (not narrowed). Ignored for auto_decline.
    approve_subset       TEXT[]      NOT NULL DEFAULT '{}',
    -- A four-eyes marker beats auto_approve: a matching request is queued for two
    -- humans rather than auto-approved. Dual control is a floor a policy can't waive.
    four_eyes            BOOLEAN     NOT NULL DEFAULT false,
    -- Explicit author order; the lowest priority (then oldest) matching policy wins.
    priority             INTEGER     NOT NULL DEFAULT 0,
    created_by           UUID REFERENCES users (id) ON DELETE SET NULL,
    -- Validity window is carried from day one but not enforced in v1 (org-wide,
    -- no window, mirroring the RBAC seam); #27 turns window enforcement on.
    valid_from           TIMESTAMPTZ,
    valid_until          TIMESTAMPTZ,
    -- Revocation is immediate: a revoked policy is skipped by the matcher at once.
    revoked_at           TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The matcher lists active policies for one org + kind in evaluation order.
CREATE INDEX idx_policies_matching
    ON policies (organization_id, kind, priority, created_at)
    WHERE revoked_at IS NULL;

-- +goose Down
DROP TABLE policies;
