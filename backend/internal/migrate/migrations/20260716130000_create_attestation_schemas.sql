-- +goose Up
-- Credential-type definitions an organization can issue (the "Schemas" tab). The
-- vct is the org-namespaced credential type shown in the UI; credential_config_id
-- is the issuer-registered credential this maps to (the Veramo credentialId).
-- attributes is the ordered allow-list of fields ([{key,label,type,required}]) —
-- the data-minimisation boundary for issuance (Art 5(1)(b)).
CREATE TABLE attestation_schemas
(
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id      UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    vct                  TEXT        NOT NULL,
    display_name         TEXT        NOT NULL,
    credential_config_id TEXT        NOT NULL,
    -- Who the credential is about, and therefore how it is delivered: a natural
    -- person is notified by e-mail, an organization is offered over QERDS to its
    -- business wallet (its digital address in the address book).
    subject_type         TEXT        NOT NULL DEFAULT 'natural_person' CHECK (subject_type IN ('natural_person', 'organization')),
    attributes           JSONB       NOT NULL DEFAULT '[]'::jsonb,
    qualified            BOOLEAN     NOT NULL DEFAULT false,
    status               TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('draft', 'active', 'deprecated')),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_attestation_schemas_org_vct ON attestation_schemas (organization_id, vct);

-- +goose Down
DROP TABLE attestation_schemas;
