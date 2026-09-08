-- +goose Up
-- Phone number and identity-verification state for members. Both are set when a
-- person accepts an invitation by disclosing a passport / id-card credential
-- (plus phone) through the identity flow. identity_verified_at is orthogonal to
-- the member view's active/invited status: an active member may or may not be
-- verified, and NULL means never verified (no re-identification has happened
-- yet, or the membership predates this column and was never backfilled).
-- date_of_birth is disclosed alongside the identity credential and kept for
-- future document-to-person comparisons (e.g. screening matches).
-- identity_reviews carries the disclosed phone and date of birth so a member
-- admitted via an admin-approved (name-mismatch) review still gets them on the
-- resulting membership.
ALTER TABLE memberships
    ADD COLUMN phone                TEXT,
    ADD COLUMN identity_verified_at TIMESTAMPTZ,
    ADD COLUMN date_of_birth        DATE;

ALTER TABLE identity_reviews
    ADD COLUMN phone         TEXT,
    ADD COLUMN date_of_birth DATE;

-- +goose Down
ALTER TABLE memberships
    DROP COLUMN phone,
    DROP COLUMN identity_verified_at,
    DROP COLUMN date_of_birth;

ALTER TABLE identity_reviews
    DROP COLUMN phone,
    DROP COLUMN date_of_birth;
