-- +goose Up
-- The approval queue (consent-approval-layer.md, #113). Generalises the
-- identity_reviews pending -> decided + audit precedent to two payload kinds:
-- 'presentation' (disclose attributes to a verifier) and 'issuance' (accept a
-- held credential from an issuer). Unlike identity_reviews it persists the decided
-- status, since the decided item is itself the record audited against. An
-- un-actioned item expires rather than lingering (invitation-expiry precedent).
--
-- decided_subset records the attribute-level outcome Annex 12(2) asks for: an
-- approver may approve a subset of what was requested. It is empty until (and
-- unless) the item is approved.
CREATE TABLE approval_requests
(
    id              UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    kind            TEXT        NOT NULL CHECK (kind IN ('presentation', 'issuance')),
    -- The verifier (presentation) or issuer (issuance) identity, as far as the
    -- protocol authenticates it.
    counterparty    TEXT        NOT NULL,
    -- The attribute set requested / offered: the reviewable payload.
    requested       TEXT[]      NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'approved', 'declined', 'expired', 'superseded_by_policy')),
    -- Which of the four modes resolved this item. 'human_initiated' never enters
    -- the queue (initiation is consent); 'superseded_by_policy' is reserved for the
    -- on-author re-evaluation follow-up. Both are carried for model completeness.
    mode            TEXT        NOT NULL
        CHECK (mode IN ('human_initiated', 'human_approved', 'policy_auto', 'four_eyes')),
    -- The approved attribute subset (empty unless approved).
    decided_subset  TEXT[]      NOT NULL DEFAULT '{}',
    decided_by      UUID REFERENCES users (id) ON DELETE SET NULL,
    decided_at      TIMESTAMPTZ,
    -- Second approver, four-eyes only; NULL otherwise.
    dual_decided_by UUID REFERENCES users (id) ON DELETE SET NULL,
    dual_decided_at TIMESTAMPTZ,
    -- The policy that auto-decided this item, when mode = 'policy_auto'.
    policy_id       UUID REFERENCES policies (id) ON DELETE SET NULL,
    -- The request's own validity; an un-actioned item expires at this time.
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_approval_requests_pending
    ON approval_requests (organization_id, created_at)
    WHERE status = 'pending';

-- +goose Down
DROP TABLE approval_requests;
