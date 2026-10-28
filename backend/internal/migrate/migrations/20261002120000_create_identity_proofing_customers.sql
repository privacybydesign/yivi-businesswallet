-- +goose Up
-- identity_proofing_customers are the org's own B2B customers: the parties the
-- org proofs external people (subjects) for. A customer has no login and no
-- wallet account; the org's members act for it, and its backend can use the
-- public /proofing API with an API key (identity_proofing_api_keys). The flows
-- stay the org's own; a customer gets a subset of them assigned
-- (identity_proofing_customer_flows).
CREATE TABLE identity_proofing_customers
(
    id                       UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    organization_id          UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name                     TEXT        NOT NULL CHECK (btrim(name) <> ''),
    created_by               UUID        REFERENCES users (id) ON DELETE SET NULL,
    -- When an admin paused proofing for the customer: while set, no new
    -- request can be sent for it. Requests already sent run out as usual.
    paused_at                TIMESTAMPTZ,
    -- How long a session runs, counted from its creation (2, 5 or 10 minutes).
    session_ttl_seconds      INTEGER     NOT NULL DEFAULT 600 CHECK (session_ttl_seconds IN (120, 300, 600)),
    -- How long a session's personal data is kept after it ends before the
    -- pruner clears it. The app offers 7, 30, 90, 180 or 365
    -- (proofing.DataRetentionDayOptions); the database holds the bound.
    data_retention_days      INTEGER     NOT NULL DEFAULT 30 CHECK (data_retention_days BETWEEN 1 AND 365),
    -- Branding the proofing mail and hosted page carry in place of the org's:
    -- the name it signs with (NULL is the customer's name), its primary colour
    -- (NULL is the org's), its logo (NULL shows the name as a wordmark), and a
    -- support contact and privacy statement URL (NULL leaves each out).
    display_name             TEXT CHECK (btrim(display_name) <> ''),
    primary_color            TEXT CHECK (primary_color ~ '^#[0-9a-fA-F]{6}$'),
    logo_bytes               BYTEA,
    logo_content_type        TEXT,
    support_contact          TEXT CHECK (btrim(support_contact) <> ''),
    privacy_url              TEXT CHECK (privacy_url ~ '^https://'),
    -- Leaves the "powered by" line off its hosted pages.
    hide_powered_by          BOOLEAN     NOT NULL DEFAULT false,
    -- The origins a hosted page may redirect its subject to and be embedded on.
    allowed_redirect_origins TEXT[]      NOT NULL DEFAULT '{}',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- The composite key lets the rows below prove their customer is of the
    -- same org.
    UNIQUE (organization_id, id),
    CONSTRAINT identity_proofing_customers_check CHECK ((logo_bytes IS NULL) = (logo_content_type IS NULL))
);

CREATE UNIQUE INDEX identity_proofing_customers_org_name_idx
    ON identity_proofing_customers (organization_id, lower(name));

-- +goose Down
DROP TABLE identity_proofing_customers;
