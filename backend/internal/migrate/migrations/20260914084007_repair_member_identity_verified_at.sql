-- +goose Up
-- Repair for 20260716150000_add_member_identity.sql, which was rewritten in
-- place (PR #243) after it had already been applied. Its first version added
-- `identity_verified BOOLEAN NOT NULL DEFAULT false`; the rewrite renamed that to
-- `identity_verified_at TIMESTAMPTZ` and added `date_of_birth` to memberships
-- and identity_reviews. Goose records versions, not contents, so a database
-- migrated between the two (dev volumes, staging) kept the boolean and every
-- members query fails with `column m.identity_verified_at does not exist`.
--
-- A database that ran the rewritten file already has the target shape, so every
-- statement here must be a no-op there: IF [NOT] EXISTS for the columns, and a
-- PL/pgSQL block for the statements that name the legacy column (PL/pgSQL only
-- parses a statement when it first executes it, so the UPDATE never compiles on a
-- database where the column does not exist).
--
-- The boolean was only ever set true on the INSERT that created the membership
-- (invitation accept, admin-approved identity review), so created_at is the
-- moment that identity was verified and the backfill is exact. identity_due_at
-- is left NULL, matching what those rows would have had: no org identity policy
-- existed before the rewrite, and saving one recomputes due dates.
ALTER TABLE memberships
    ADD COLUMN IF NOT EXISTS identity_verified_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS date_of_birth        DATE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name   = 'memberships'
          AND column_name  = 'identity_verified'
    ) THEN
        UPDATE memberships
           SET identity_verified_at = created_at
         WHERE identity_verified
           AND identity_verified_at IS NULL;

        ALTER TABLE memberships DROP COLUMN identity_verified;
    END IF;
END
$$;
-- +goose StatementEnd

ALTER TABLE identity_reviews
    ADD COLUMN IF NOT EXISTS date_of_birth DATE;

-- +goose Down
-- Intentionally empty. This migration converges two schema histories onto the
-- shape 20260716150000 now describes; reverting it would have to pick one
-- history to restore and would break the application either way. Rolling back
-- past it is handled by 20260716150000's own Down.
