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

-- Dropping the column drops its composite foreign keys (to the customer and to
-- the API key, which the migration for the keys replaces), the composite
-- unique key, the checks that name it and the indexes that lead with it.
ALTER TABLE identity_proofing_requests
    DROP COLUMN customer_id;

-- Only an external person's request is created through an API key.
ALTER TABLE identity_proofing_requests
    ADD CONSTRAINT identity_proofing_requests_api_key_check
        CHECK (api_key_id IS NULL OR subject_kind = 'external');

-- The identity match key hashed the customer in. It hashes the org now, and the
-- family name and date of birth it was made of are not kept, so the keys held
-- cannot be made again: a session settled before this is not found by a data
-- request, which only reads sessions that carry the requester's key.
UPDATE identity_proofing_requests SET identity_match_key = NULL WHERE identity_match_key IS NOT NULL;

-- A data request's candidates are its org's external sessions of the same
-- person.
CREATE INDEX identity_proofing_requests_identity_match_idx
    ON identity_proofing_requests (organization_id, identity_match_key)
    WHERE identity_match_key IS NOT NULL;

-- +goose Down
ALTER TABLE identity_proofing_requests
    ADD COLUMN customer_id UUID;

-- The customers table is recreated, one per org, by the migration that drops it.
UPDATE identity_proofing_requests r
SET customer_id = (SELECT c.id FROM identity_proofing_customers c WHERE c.organization_id = r.organization_id)
WHERE r.subject_kind = 'external';

-- The identity match keys cannot be restored: they hashed the customer.
DROP INDEX identity_proofing_requests_identity_match_idx;
UPDATE identity_proofing_requests SET identity_match_key = NULL WHERE identity_match_key IS NOT NULL;

ALTER TABLE identity_proofing_requests
    DROP CONSTRAINT identity_proofing_requests_api_key_check,
    DROP CONSTRAINT identity_proofing_requests_subject_kind_user_check,
    DROP COLUMN subject_kind;

ALTER TABLE identity_proofing_requests
    ADD UNIQUE (organization_id, customer_id, id),
    ADD FOREIGN KEY (organization_id, customer_id)
        REFERENCES identity_proofing_customers (organization_id, id) ON DELETE RESTRICT,
    ADD FOREIGN KEY (organization_id, customer_id, api_key_id)
        REFERENCES identity_proofing_api_keys (organization_id, customer_id, id) ON DELETE SET NULL (api_key_id),
    ADD CONSTRAINT identity_proofing_requests_api_key_check
        CHECK (api_key_id IS NULL OR customer_id IS NOT NULL),
    ADD CONSTRAINT identity_proofing_requests_check1 CHECK (customer_id IS NULL OR subject_user_id IS NULL);

CREATE INDEX identity_proofing_requests_org_customer_idx
    ON identity_proofing_requests (organization_id, customer_id, created_at DESC);
CREATE INDEX identity_proofing_requests_identity_match_idx
    ON identity_proofing_requests (organization_id, customer_id, identity_match_key)
    WHERE identity_match_key IS NOT NULL;
