-- +goose Up
-- A flow's own branding, over the org's (identity_proofing_settings). A row
-- is "custom for this flow"; no row is "same as the organisation". Each field
-- left NULL falls back to the org's, and the logo (NULL) to the org's logo. It
-- styles the flow's proofing mail and hosted page; "powered by" stays the
-- org's.
--
-- The flows belong to the proofing provider, so flow_id has no foreign key
-- (as in identity_proofing_flow_settings); a flow the provider no longer lists
-- is ignored on read.
CREATE TABLE identity_proofing_flow_branding
(
    organization_id   UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    flow_id           TEXT        NOT NULL,
    display_name      TEXT CHECK (btrim(display_name) <> ''),
    primary_color     TEXT CHECK (primary_color ~ '^#[0-9a-fA-F]{6}$'),
    logo_bytes        BYTEA,
    logo_content_type TEXT,
    support_contact   TEXT CHECK (btrim(support_contact) <> ''),
    privacy_url       TEXT CHECK (privacy_url ~ '^https://'),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, flow_id),
    CONSTRAINT identity_proofing_flow_branding_logo_check CHECK ((logo_bytes IS NULL) = (logo_content_type IS NULL))
);

-- +goose Down
DROP TABLE identity_proofing_flow_branding;
