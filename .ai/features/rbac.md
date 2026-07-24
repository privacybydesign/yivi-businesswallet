# RBAC — roles, permissions and the enforcement seam

**Status:** v1 implemented (vocabulary + enforcement seam). Real-time mandate/delegation enforcement is #27.
**Regulation:** COM(2025) 838 Recital 18; Art 3(18)(19), 5(1); Annex §12.
**Design of record:** `.ai/plans/rbac-model.md` (issue #115, epic #27). Read it for the *why*; this doc is the *what shipped*.

---

## 1. What this is

Authorisation used to collapse to one question: *is the caller an admin?* `RequireOrgAdmin` gated
every privileged route on `role == RoleAdmin`. This slice replaces that binary gate with a
resource × action permission matrix and a `RequirePermission(resource, action)` seam, keeping the
two axes the regulation separates apart:

- **Axis A — basis of authority.** On what legal footing may the subject act at all (register-backed
  legal representative, full or administrative mandate, delegation). Rooted in `wallet_representations`.
  Its enforcement engine is #27; nothing here grants an Axis-A capability.
- **Axis B — functional role.** Which resources may the subject operate. This is what shipped.

## 2. Functional roles (fixed, code-defined)

`internal/organization/organization.go` defines five assignable roles. `admin` and `member` keep
their existing string values so no membership rows or bootstrap paths migrate; the other three are
additive.

| role | holds |
|---|---|
| `admin` | the full administrative-mandate surface: all `members`, `attestations`, `qerds`, `settings`, plus `audit:read` |
| `member` | `attestations:read`, `qerds:read` |
| `attestation_issuer` | `attestations:{read,issue,cancel_offer,revoke}` |
| `qerds_operator` | `qerds:{read,provision_address,send}` |
| `auditor` | read-only across the org: `{members,attestations,qerds,settings}:read` and `audit:read` |

Platform admin (`auth.PlatformAdmins`) stays deployment-level and orthogonal; it is not an org role.

## 3. The permission matrix (`internal/organization/permissions.go`)

A permission is a `"{resource}:{action}"` string. `rolePermissions` is the compiled, single source of
truth — auditable by construction (it lives in git, `permissions_test.go` pins every grant). Two
deliberate choices, both to preserve pre-RBAC behaviour while adding the finer roles:

- **`member` does not hold `members:read`.** Reading the member directory is an admin/auditor
  capability in this product (the member-list endpoints were admin-gated); a plain member reads only
  the org it belongs to, via the ungated `GET /orgs/{slug}`.
- **No functional role holds any `mandates:*` or `wallet:*` permission** — not even `admin`. Those are
  Axis-A-gated and reachable only through #27's mandate lifecycle, never through a role.
  `TestNoFunctionalRoleHoldsAxisAPermission` enforces this.

## 4. The enforcement seam

`RequirePermission(resource, action)` reads the effective role stashed by `Authorize` (never the
request), resolves it against `rolePermissions`, and forbids when the permission is absent. v1 checks
role → permission with **org-wide scope and no validity window**, so for `admin` it is behaviourally
identical to the old `RequireOrgAdmin`, plus the finer roles. Scope narrowing, validity windows and
the Axis-A checks are the seams #27 fills behind this signature, which does not change when it does.

Migrated to `RequirePermission` in this slice:
- `internal/organization` — member management (`members:*`) and the org audit log (`audit:read`).
- `internal/qerds` — address provisioning (`qerds:provision_address`).
- `internal/attestation` — issue / cancel-offer / revoke (`attestations:{issue,cancel_offer,revoke}`);
  schema/template/key management stays admin-only (`manage_templates` / `manage_keys`).

`RequireOrgAdmin` is retained as a thin alias over the administrative-mandate role for the routes not
yet migrated: the WSCA/wallet lifecycle (Axis-A-gated, must stay admin until #27), org settings
(no distinct functional role), department structure, and org rename. Migrating those is incremental
and safe because the alias behaves exactly as before.

## 5. Assignment and the last-admin guard

The new roles are assignable through the existing paths — at invite time (`Invitation.Role`) and via
`UpdateMembership` — validated by `IsAssignableRole`. The last-admin guard in `member_store.go` now
fires on *any* demotion off `admin` (to a finer role, not only to `member`), so the finer roles cannot
be used to leave an org with no administrator.

Mandate objects, delegation chains, scope/validity windows and the audit binding to verifiable proofs
are **not** in this slice — they are #27. The frontend role pickers still offer only admin/member; the
finer roles are API-assignable and gain UI selection in a follow-up.
