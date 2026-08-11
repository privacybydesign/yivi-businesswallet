-- +goose Up
-- Mandates granted inside the wallet (Recital 18, Art 3(19), Art 5(1)(j)).
--
-- This is the "granted mandate" half of Axis A in .ai/plans/rbac-model.md. The
-- register-backed half already lives in wallet_representations: those rows come
-- from the KVK registration attestation and cannot be minted here. A row in this
-- table is authority that arose *inside* the wallet — the owner, acting through a
-- legal representative (a claimed `bestuurder` representation), grants a mandate
-- to one of its members, and a mandate holder may delegate onward.
--
-- `type` is the Recital 18 tier: a `full` mandate lets the grantee act on the
-- owner's behalf generally; an `administrative` mandate lets them assign roles and
-- responsibilities within the scope below. `full` strictly contains
-- `administrative`, which is what bounds a delegation (a holder cannot delegate
-- more than they hold).
--
-- There is no `status` column. The lifecycle (grant -> active -> revoked/expired)
-- is derived from revoked_at and the validity window against the clock at read
-- time, so an expiry takes effect without a sweep touching the row — Annex
-- §12(3)(b) wants expired authorisations rejected in real time, and a status
-- column would only be as fresh as the last job that wrote it.
--
-- A revocation is either immediate (revoked_at = now()) or effective-dated (a
-- valid_until in the future), which is why both columns exist rather than one.
CREATE TABLE mandates
(
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    type                TEXT        NOT NULL,
    -- The natural person who granted it. Nullable only so purging a user does not
    -- take the mandate register with it; the audit trail keeps the actor.
    grantor_user_id     UUID        REFERENCES users (id) ON DELETE SET NULL,
    grantee_user_id     UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Scope bounds where the mandate reaches. 'organization' is org-wide;
    -- 'department' narrows it to one department, and only an org-wide mandate
    -- carries org-wide administrative authority.
    scope               TEXT        NOT NULL DEFAULT 'organization',
    scope_department_id UUID,
    -- Set when this mandate was delegated from another one. The chain is what
    -- makes over-delegation checkable and what a revocation cascades down.
    parent_mandate_id   UUID        REFERENCES mandates (id) ON DELETE CASCADE,
    valid_from          TIMESTAMPTZ NOT NULL DEFAULT now(),
    valid_until         TIMESTAMPTZ,
    revoked_at          TIMESTAMPTZ,
    revoked_by_user_id  UUID        REFERENCES users (id) ON DELETE SET NULL,
    revocation_reason   TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT mandates_type_check CHECK (type IN ('full', 'administrative')),
    CONSTRAINT mandates_scope_check CHECK (
        (scope = 'organization' AND scope_department_id IS NULL) OR
        (scope = 'department' AND scope_department_id IS NOT NULL)),
    CONSTRAINT mandates_window_check CHECK (valid_until IS NULL OR valid_until > valid_from),
    CONSTRAINT mandates_no_self_parent CHECK (parent_mandate_id IS NULL OR parent_mandate_id <> id),
    -- Composite, like memberships_department_fkey, so a mandate cannot be scoped
    -- to another organization's department. Named so the store can tell this apart
    -- from the other foreign-key violations on this table.
    CONSTRAINT mandates_department_fkey FOREIGN KEY (scope_department_id, organization_id)
        REFERENCES departments (id, organization_id) ON DELETE CASCADE
);

-- The mandate register for one org, and the per-caller authority lookup Authorize
-- runs on every org-scoped request.
CREATE INDEX idx_mandates_organization ON mandates (organization_id);
CREATE INDEX idx_mandates_grantee ON mandates (organization_id, grantee_user_id);
-- Walks the delegation chain when a revocation cascades.
CREATE INDEX idx_mandates_parent ON mandates (parent_mandate_id);

-- +goose Down
DROP TABLE mandates;
