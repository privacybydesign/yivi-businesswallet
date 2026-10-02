-- +goose Up
-- What the wallet keeps per flow of an org, in one row: whether members may
-- send requests on it and which flow the request form preselects, how its
-- hosted page behaves, whether it asks for DUO diploma extracts, and what its
-- sessions are for.
--
-- The flows themselves belong to the proofing provider (the engine's
-- identity_proofing_flow_versions, or the in-memory stub), so flow_id has no
-- foreign key; a flow the provider no longer lists is ignored on read. No row
-- reads as every default: not offered to members, hosted page on in every
-- language and ending on the session's redirect, diplomas off, an identity
-- check.
CREATE TABLE identity_proofing_flow_settings
(
    organization_id   UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    flow_id           TEXT        NOT NULL,
    -- Members may send requests on the flow; one allowed flow is the default.
    member_allowed    BOOLEAN     NOT NULL DEFAULT false,
    member_default    BOOLEAN     NOT NULL DEFAULT false,
    -- Whether hosted links may be made for the flow, the languages its page
    -- offers (empty: every supported one), and whether it ends on the
    -- session's redirect or always on its own thank-you page.
    hosted_enabled    BOOLEAN     NOT NULL DEFAULT true,
    hosted_locales    TEXT[]      NOT NULL DEFAULT '{}',
    hosted_completion TEXT        NOT NULL DEFAULT 'redirect' CHECK (hosted_completion IN ('redirect', 'done')),
    -- Whether the subject uploads DUO diploma extracts after the identity check.
    diplomas          TEXT        NOT NULL DEFAULT 'off' CHECK (diplomas IN ('off', 'required')),
    -- An identity check, or a customer's subject asking for their data
    -- ("see my data") or its erasure ("delete my data"): never a members' flow.
    kind              TEXT        NOT NULL DEFAULT 'identity'
        CHECK (kind IN ('identity', 'data_access', 'data_erasure')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, flow_id),
    CHECK (member_allowed OR NOT member_default),
    CHECK (kind = 'identity' OR NOT member_allowed)
);

CREATE UNIQUE INDEX identity_proofing_flow_settings_one_member_default_idx
    ON identity_proofing_flow_settings (organization_id) WHERE member_default;

-- +goose Down
DROP TABLE identity_proofing_flow_settings;
