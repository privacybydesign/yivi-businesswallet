-- +goose Up
-- The reviewer right (#327): a member an admin lets decide identity checks and
-- data requests under Identity proofing > Reviews. Admins decide without it, so
-- the column only matters while role = 'member'.
ALTER TABLE memberships
    ADD COLUMN can_review BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE memberships
    DROP COLUMN can_review;
