-- +goose Up
-- Phone number and identity-verified flag for members. Both are set when a person
-- accepts an invitation by disclosing a passport / id-card credential (plus phone)
-- through the identity flow. identity_verified is orthogonal to the member view's
-- active/invited status: an active member may or may not be verified.
-- identity_reviews carries the disclosed phone so a member admitted via an
-- admin-approved (name-mismatch) review still gets it on the resulting membership.
ALTER TABLE memberships
    ADD COLUMN phone             TEXT,
    ADD COLUMN identity_verified BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE identity_reviews
    ADD COLUMN phone TEXT;

-- +goose Down
ALTER TABLE memberships
    DROP COLUMN phone,
    DROP COLUMN identity_verified;

ALTER TABLE identity_reviews
    DROP COLUMN phone;
