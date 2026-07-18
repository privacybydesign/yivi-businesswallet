-- +goose Up
-- Named issuance presets over a schema (the "Templates" tab). default_attributes
-- pre-fills the issue wizard; validity_seconds sets the issued credential's
-- lifetime; key_material_id chooses which key seals it (NULL = org default).
-- linked_schema_ids records chained/linked attestations (Art 5(1)(g)); the wizard
-- step for it is not built yet, the column keeps the model forward-compatible.
CREATE TABLE attestation_templates
(
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id    UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    schema_id          UUID        NOT NULL REFERENCES attestation_schemas (id) ON DELETE CASCADE,
    name               TEXT        NOT NULL,
    default_attributes JSONB,
    validity_seconds   INTEGER,
    key_material_id    UUID        REFERENCES attestation_keys (id) ON DELETE SET NULL,
    linked_schema_ids  UUID[],
    status             TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_attestation_templates_organization_id ON attestation_templates (organization_id);
CREATE INDEX idx_attestation_templates_schema_id ON attestation_templates (schema_id);

-- +goose Down
DROP TABLE attestation_templates;
