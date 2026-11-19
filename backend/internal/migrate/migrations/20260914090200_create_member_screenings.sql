-- +goose Up
-- member_screenings is one row per VOG check attempt (#242): history survives,
-- so a member's coverage under a past requirement stays visible even after the
-- org's required codes change. Only the check's outcome and the org-required
-- code subset are kept - never the PDF, never place of birth, purpose or the
-- full code list on the document, per #242's data-minimisation design. A
-- re-check always means a new upload or a new credential disclosure; there is
-- no re-validation of a stored document, because none is stored.
--
-- covered_codes / missing_codes are the *org-required* codes at check time that
-- the document did / did not cover - not the document's full code list. This
-- has one consequence worth its own record: when an org later adds a required
-- code, an old "valid" row cannot tell whether the document covered it, so
-- DeriveScreeningStatus (internal/organization) treats that as recheck_required
-- rather than silently staying valid.
-- reference_hash is a keyed hash of the kenmerk (never the raw reference), so
-- the same document being re-uploaded is detectable without keeping the number.
-- checked_by / checked_by_user_id record who ran the check - the member
-- themself, or an admin uploading on the member's behalf.
-- gaav_response_code is validatie.nl's raw code, kept for diagnostics only.
--
-- Composite FK to memberships (like identity_reverify_tokens): a screening
-- record only makes sense for an actual membership, and follows it - removing a
-- member removes their screening history with it, the same operational-record
-- posture as the rest of the membership lifecycle (audit_events is the trail
-- that survives, not this table).
CREATE TABLE member_screenings
(
    id                 UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    organization_id    UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id            UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    method             TEXT        NOT NULL CHECK (method IN ('pdf', 'yivi_credential')),
    result             TEXT        NOT NULL CHECK (result IN ('valid', 'rejected', 'mismatch', 'insufficient_scope')),
    checked_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    vog_issue_date     DATE,
    valid_until        TIMESTAMPTZ,
    covered_codes      TEXT[]      NOT NULL DEFAULT '{}',
    missing_codes      TEXT[]      NOT NULL DEFAULT '{}',
    reference_hash     BYTEA,
    checked_by         TEXT        NOT NULL CHECK (checked_by IN ('self', 'admin')),
    checked_by_user_id UUID REFERENCES users (id) ON DELETE SET NULL,
    gaav_response_code INT,
    CONSTRAINT member_screenings_membership_fkey FOREIGN KEY (user_id, organization_id) REFERENCES memberships (user_id, organization_id) ON DELETE CASCADE
);

-- The member list and the "latest screening" lookup both select the most recent
-- row for (org, member) - covered by this index's leading columns and its DESC
-- order.
CREATE INDEX idx_member_screenings_latest ON member_screenings (organization_id, user_id, checked_at DESC);

-- +goose Down
DROP TABLE member_screenings;
