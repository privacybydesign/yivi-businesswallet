-- +goose Up
-- identity_proofing_customers are the org's own B2B customers: the parties the
-- org proofs external people (subjects) for. A customer has no login and no
-- wallet account; the org's members act for it. The flows stay the org's own at
-- IPS - a customer gets a subset of them assigned, with one default, in
-- identity_proofing_customer_flows (the same shape as the members' allow-list in
-- org_identity_proofing_flows). A flow id IPS no longer lists is ignored on read.
CREATE TABLE identity_proofing_customers
(
    id              UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name            TEXT        NOT NULL CHECK (btrim(name) <> ''),
    created_by      UUID        REFERENCES users (id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- The composite key lets the flow and request rows prove their customer is
    -- of the same org.
    UNIQUE (organization_id, id)
);

CREATE UNIQUE INDEX identity_proofing_customers_org_name_idx
    ON identity_proofing_customers (organization_id, lower(name));

CREATE TABLE identity_proofing_customer_flows
(
    organization_id UUID        NOT NULL,
    customer_id     UUID        NOT NULL,
    flow_id         TEXT        NOT NULL,
    is_default      BOOLEAN     NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, flow_id),
    FOREIGN KEY (organization_id, customer_id)
        REFERENCES identity_proofing_customers (organization_id, id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX identity_proofing_customer_flows_one_default_idx
    ON identity_proofing_customer_flows (customer_id) WHERE is_default;

-- A request now goes either to a member (subject_user_id) or to a customer's
-- subject (customer_id): an external person known only by an e-mail address and
-- an optional name, never a wallet user. subject_name is '' when the member who
-- sent it gave no name.
--
-- proofed_name_ciphertext is the name IPS read off the subject's document, kept
-- only for a customer's subject (a member already has a name) and sealed with
-- IDENTITY_PROOFING_ENCRYPTION_KEY. It is the one document field the wallet keeps,
-- and only until proofed_name_purge_after, when the pruner clears it.
ALTER TABLE identity_proofing_requests
    ADD COLUMN customer_id              UUID,
    ADD COLUMN proofed_name_ciphertext  BYTEA,
    ADD COLUMN proofed_name_purge_after TIMESTAMPTZ,
    ADD FOREIGN KEY (organization_id, customer_id)
        REFERENCES identity_proofing_customers (organization_id, id) ON DELETE RESTRICT,
    ADD CHECK (customer_id IS NULL OR subject_user_id IS NULL),
    ADD CHECK ((proofed_name_ciphertext IS NULL) = (proofed_name_purge_after IS NULL));

CREATE INDEX identity_proofing_requests_org_customer_idx
    ON identity_proofing_requests (organization_id, customer_id, created_at DESC);
CREATE INDEX identity_proofing_requests_proofed_name_purge_idx
    ON identity_proofing_requests (proofed_name_purge_after) WHERE proofed_name_purge_after IS NOT NULL;

-- +goose Down
DROP INDEX identity_proofing_requests_proofed_name_purge_idx;
DROP INDEX identity_proofing_requests_org_customer_idx;
ALTER TABLE identity_proofing_requests
    DROP COLUMN proofed_name_purge_after,
    DROP COLUMN proofed_name_ciphertext,
    DROP COLUMN customer_id;
DROP TABLE identity_proofing_customer_flows;
DROP TABLE identity_proofing_customers;
