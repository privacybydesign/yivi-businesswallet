-- +goose Up
-- The reviewer right is a member's: an admin reviews by their role, and a role
-- change clears the right (organization.Store.UpdateMembership), so a right
-- left on an admin never comes back with a demotion nobody gave it for. Clear
-- one an earlier build left on an admin, then hold every row to it.
UPDATE memberships SET can_review = false WHERE can_review AND role <> 'member';

ALTER TABLE memberships
    ADD CONSTRAINT memberships_can_review_member CHECK (NOT can_review OR role = 'member');

-- +goose Down
-- The rights the Up cleared on admins are not restored: no admin needs one.
ALTER TABLE memberships
    DROP CONSTRAINT memberships_can_review_member;
