-- +goose Up
-- A request goes to a member of the org or to an external person, who is no
-- member: that is what the customer_id told apart. subject_user_id cannot,
-- since it is cleared when the member's user is removed.
ALTER TABLE identity_proofing_requests
    ADD COLUMN subject_kind TEXT NOT NULL DEFAULT 'member' CHECK (subject_kind IN ('member', 'external'));

UPDATE identity_proofing_requests SET subject_kind = 'external' WHERE customer_id IS NOT NULL;

ALTER TABLE identity_proofing_requests
    ADD CONSTRAINT identity_proofing_requests_subject_kind_user_check
        CHECK (subject_kind = 'member' OR subject_user_id IS NULL);

DROP INDEX identity_proofing_requests_org_customer_idx;

-- Dropping the column drops its composite foreign key and the check that a
-- request had a customer or a member, never both.
ALTER TABLE identity_proofing_requests
    DROP COLUMN customer_id;

-- +goose Down
ALTER TABLE identity_proofing_requests
    ADD COLUMN customer_id UUID;

-- The customers table is recreated, one per org, by the migration that drops it.
UPDATE identity_proofing_requests r
SET customer_id = (SELECT c.id FROM identity_proofing_customers c WHERE c.organization_id = r.organization_id)
WHERE r.subject_kind = 'external';

ALTER TABLE identity_proofing_requests
    ADD FOREIGN KEY (organization_id, customer_id)
        REFERENCES identity_proofing_customers (organization_id, id) ON DELETE RESTRICT,
    ADD CONSTRAINT identity_proofing_requests_check1 CHECK (customer_id IS NULL OR subject_user_id IS NULL),
    DROP CONSTRAINT identity_proofing_requests_subject_kind_user_check,
    DROP COLUMN subject_kind;

CREATE INDEX identity_proofing_requests_org_customer_idx
    ON identity_proofing_requests (organization_id, customer_id, created_at DESC);
