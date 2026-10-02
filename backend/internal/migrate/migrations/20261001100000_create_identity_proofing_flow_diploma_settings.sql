-- +goose Up
-- Whether a flow asks its subject for their DUO diploma extracts after the
-- identity check, per org (flows live at IPS): off, optional or required.
-- No row is off.
CREATE TABLE identity_proofing_flow_diploma_settings (
    organization_id UUID NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    flow_id         TEXT NOT NULL,
    diplomas        TEXT NOT NULL CHECK (diplomas IN ('off', 'optional', 'required')),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, flow_id)
);

-- +goose Down
DROP TABLE identity_proofing_flow_diploma_settings;
