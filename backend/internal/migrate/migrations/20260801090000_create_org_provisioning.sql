-- +goose Up
-- org_provisioning_settings is the per-organization configuration of the
-- directory sync: which source, the app registration to authenticate as, which
-- group is in scope, and which groups map to the admin role. The client secret is
-- stored encrypted at rest (AES-256-GCM under the deployment provisioning
-- encryption key); the row never holds the plaintext.
--
-- One row per organization, not one per source: an organization provisions from
-- one authoritative directory. Adding a second source later means relaxing the
-- primary key, not reshaping the row.
CREATE TABLE org_provisioning_settings
(
    organization_id          UUID        PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    source                   TEXT        NOT NULL,
    enabled                  BOOLEAN     NOT NULL DEFAULT false,
    tenant_id                TEXT        NOT NULL DEFAULT '',
    client_id                TEXT        NOT NULL DEFAULT '',
    client_secret_ciphertext BYTEA,
    -- Empty scopes the sync to the whole directory; admin_groups is a JSON array
    -- of source group ids, edited and read as one document by one settings screen.
    group_id                 TEXT        NOT NULL DEFAULT '',
    admin_groups             JSONB       NOT NULL DEFAULT '[]',
    last_run_at              TIMESTAMPTZ,
    last_run_status          TEXT        NOT NULL DEFAULT '',
    last_run_error           TEXT        NOT NULL DEFAULT '',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- provisioned_members records which memberships and invitations the sync owns.
-- It is what keeps a directory sync and the manual invitation flow in one
-- organization: a row with no link here is never touched, so an account added by
-- hand is not deprovisioned when it is absent from the source.
--
-- The link is (source object id -> e-mail) rather than an invitation or user id:
-- an invitation becomes a membership when the person accepts, and the e-mail is
-- the identifier those two rows share. Deleting the organization takes the links
-- with it; nothing else references them.
CREATE TABLE provisioned_members
(
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    source          TEXT        NOT NULL,
    external_id     TEXT        NOT NULL,
    email           TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, source, external_id)
);

-- One provisioned person per e-mail per source: two directory accounts sharing a
-- mailbox would otherwise both try to own the same invitation.
CREATE UNIQUE INDEX idx_provisioned_members_email
    ON provisioned_members (organization_id, source, lower(email));

-- provisioned_departments maps a source department onto the departments row the
-- sync created for it. Without it, an admin renaming the local department would
-- make the next sync create a second one.
--
-- ON DELETE CASCADE on department_id: a department deleted by an admin drops its
-- link, and the next sync recreates it from the source. That is the honest
-- behaviour for a mirrored list — the source is authoritative for it.
CREATE TABLE provisioned_departments
(
    organization_id UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    source          TEXT        NOT NULL,
    external_id     TEXT        NOT NULL,
    department_id   UUID        NOT NULL REFERENCES departments (id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, source, external_id)
);

-- +goose Down
DROP TABLE provisioned_departments;
DROP TABLE provisioned_members;
DROP TABLE org_provisioning_settings;
