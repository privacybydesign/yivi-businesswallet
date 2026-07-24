-- +goose Up
-- Admit the RBAC functional roles (rbac-model.md, #115). The membership and
-- invitation role columns were constrained to 'admin'/'member'; the model adds
-- attestation_issuer, qerds_operator and auditor as additive functional roles an
-- administrator may assign. The constraints are the Postgres auto-named inline
-- checks (<table>_role_check).
ALTER TABLE memberships
    DROP CONSTRAINT memberships_role_check,
    ADD CONSTRAINT memberships_role_check
        CHECK (role IN ('admin', 'member', 'attestation_issuer', 'qerds_operator', 'auditor'));

ALTER TABLE invitations
    DROP CONSTRAINT invitations_role_check,
    ADD CONSTRAINT invitations_role_check
        CHECK (role IN ('admin', 'member', 'attestation_issuer', 'qerds_operator', 'auditor'));

-- +goose Down
-- Revert to the admin/member-only set. Rows carrying a widened role would violate
-- this, so a down migration is only valid once such rows are cleared.
ALTER TABLE memberships
    DROP CONSTRAINT memberships_role_check,
    ADD CONSTRAINT memberships_role_check
        CHECK (role IN ('admin', 'member'));

ALTER TABLE invitations
    DROP CONSTRAINT invitations_role_check,
    ADD CONSTRAINT invitations_role_check
        CHECK (role IN ('admin', 'member'));
