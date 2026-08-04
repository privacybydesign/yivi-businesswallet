# RBAC model: roles, permissions, scoping, assignment & revocation

Status: **proposed** (design only — not yet implemented). Issue [#115](https://github.com/privacybydesign/yivi-businesswallet/issues/115), sub-issue of the mandate & role-based authorisation epic [#27](https://github.com/privacybydesign/yivi-businesswallet/issues/27).

## Why

Every privileged action in the org slice collapses to one question: *is the caller an admin?* `Authorize` resolves `{slug}` → org, lets a platform admin through as `admin`, otherwise requires a membership and stashes its role in context (`internal/organization/middleware.go`); `RequireOrgAdmin` gates the admin-only routes on `role == RoleAdmin` (`middleware.go`, wired across `handler.go`). Roles are two strings, `RoleAdmin` / `RoleMember` (`organization.go`).

That is too coarse for what the EBW Regulation requires. An authorisation decision must weigh the acting subject's EAAs, their **formal role**, the **scope, validity and constraints of any mandate, delegation or power of attorney**, and contextual policy (Annex §12(1)); role↔attribute mappings must be verifiable, auditable, revocable and traceable to their legitimate issuers (§12(3)(a)); and role conflicts, over-delegation and expired authorisations must be detected and prevented in real time (§12(3)(b)). None of that is expressible with one admin bit.

This document specifies the model. It does **not** build the real-time enforcement engine — that is #27's grant → active → revoked/expired lifecycle. It defines the roles, the permission matrix, the scoping and validity model, the assignment/delegation/revocation lifecycles, and the enforcement *seam* those slices plug into. #113 (consent & approval) and #27 build on the vocabulary fixed here.

## The two axes

The regulation keeps two orthogonal things separate; today's flat role conflates them.

- **Axis A — basis of authority.** *On what legal footing may this subject act at all?* Register-backed legal representative, full mandate, administrative mandate, or a delegation down a chain. This is Recital 18 / Art 3(18)(19) / Annex §12(1)(c).
- **Axis B — functional role.** *Which resources may this subject operate?* The `admin` / `member` roles and their successors — Annex §12(1)(b)'s "formal role of the acting subjects within a recognised organisational structure".

A decision is a function of **(basis of authority + mandate scope/validity + functional role + resource scope)**, not a role alone (Annex §12(1)). Axis B lives *inside* the space an administrative mandate opens: the mandate says *whether* and *within what bounds* you may act; the role says *what* you may do inside those bounds.

### Axis A already has a home: `wallet_representations`

Bootstrap already writes Axis A. The KVK registration attestation lands the register's representative list in `wallet_representations` (`internal/wallet/`, migration `20260715120100`):

| column | meaning |
|---|---|
| `kind` | `bestuurder` (director — owner-grade), `gevolmachtigde` (proxy — scoped), `overig` |
| `authority` | `sole` / `jointly` (director authority), `beperkt` / `volledig` (volmacht scope) |
| `valid_from`, `valid_until` | mandate validity window |
| `claimed_by_user_id`, `claimed_at` | which user proved they are this representative (OpenID4VP) |
| `revoked_at` | mandate revocation |
| `source_message_id` | the QERDS message that carried the register attestation — the "legitimate issuer" link |

This is the register-backed root of Axis A, and it is deliberately reused rather than duplicated. `wallet.representation_claimed` / `wallet.representation_revoked` and `TargetRepresentation` already exist in the audit vocabulary (`internal/audit/audit.go`). The gaps this design fills on top of it:

1. It only records **register-derived** representatives (directors and volmachten from KVK). It has no row for an authority that arose *inside* the wallet — an administrative mandate the owner granted to an employee, or a delegation onward. Those need a first-class grant object.
2. Nothing connects a representation to a **functional role** or a **permission**. Claiming a representation grants a bare `admin` membership today (`wallet-bootstrap.md`: "granted admin regardless of representation kind"), losing the kind/authority distinction the moment authz runs.

### Axis A model

Two representative bases, distinguished by where the authority came from:

- **Register-backed representative** — a `wallet_representations` row. `kind = bestuurder` is the **legal representative** (Art 5(1) functional point 7): register/EAA-derived, not an internal grant, the root that bootstraps the wallet and the only basis permitted to grant a *full* mandate. `kind = gevolmachtigde` is a register-recorded proxy whose `authority` (`beperkt` / `volledig`) bounds it.
- **Granted mandate** — a new `mandates` object (below) for authority that arose inside the wallet: the owner (via a legal representative) grants a **full** or **administrative** mandate (Recital 18) to a grantee; a mandate holder may **delegate** onward, forming a chain (Annex §12(1)(c)).

**Grantee is a user *or* an external legal person** (Art 3(18): "a natural **or legal** person"). An accounting firm can hold a mandate. So a mandate's grantee is `(grantee_type, grantee_id)` where `grantee_type ∈ {user, organization}`. When an external org holds a mandate, its own members act under it, transitively bounded by the mandate's scope — the model carries the shape; onboarding an external legal person end-to-end is deferred (see Deferred).

```
                         owner (legal person — the org itself, grantor of all authority, Art 3(19))
                              │  grants
        register attestation  │
  KVK ───────────────────────▶├── legal representative (bestuurder)        ── register-backed, wallet_representations
                              │        │ grants full / administrative mandate
                              │        ▼
                              ├── full-mandate holder      ── mandates
                              │        │ delegates (≤ own scope)
                              │        ▼
                              └── administrative-mandate holder ──▶ assigns functional roles within scope
```

## Axis B: functional roles

**Decision: a fixed, code-defined role set for v1** (open question 1). Custom/composable roles are deferred. Reason: Annex §12(3)(a) demands role↔attribute mappings be verifiable, auditable and traceable to their issuers. A compiled `role → permissions` map is auditable by construction — it lives in git, ships with a table-driven test, and changes go through review — whereas DB-editable roles push that burden into runtime data and an admin UI the regulation would then require us to audit. When the fixed set proves too rigid, custom roles become a data-backed slice layered on the same permission vocabulary.

| role | basis of authority | what it can do |
|---|---|---|
| `admin` | administrative or full mandate | manage members, invitations, departments, attestations, QERDS, org settings, theming — the administrative-mandate surface |
| `member` | assigned by an admin | read the org; operate the resources explicitly assigned to it |
| `attestation_issuer` | assigned by an admin | the `attestations` domain (issue / cancel offer / revoke); no member or settings management |
| `qerds_operator` | assigned by an admin | the `qerds` domain (provision address, send, read inbound) |
| `auditor` | assigned by an admin | **read-only** across the org, including the audit log; no mutation anywhere |

`admin` and `member` keep their current string values (`organization.go`) so existing rows and the bootstrap path need no migration. The three new roles are additive. **Platform admin stays deployment-level and orthogonal** — it is not an org role and is not in this table (`auth.PlatformAdmins`, `RequirePlatformAdmin`).

The sensitive, owner-level capabilities — *grant/revoke a mandate*, *delegate*, *operate the wallet on the owner's behalf* — are **not** reachable from any functional role. They are gated on Axis A (legal representative or full-mandate holder), never on being `admin`. An `admin` holds an *administrative* mandate: they assign roles within their scope, they do not grant mandates.

## Permissions: resource × action

A permission is a `{resource}:{action}` string. Each role maps to a fixed set; the map is code, checked by a table-driven test that asserts every role's grants and that no functional role holds a mandate-gating permission.

| resource | actions |
|---|---|
| `members` | `read`, `invite`, `change_role`, `revoke`, `review_identity` |
| `mandates` | `grant`, `revoke`, `delegate` — **Axis-A-gated**, never granted by functional role |
| `attestations` | `read`, `issue`, `cancel_offer`, `revoke`, `manage_templates`, `manage_keys` |
| `qerds` | `read`, `provision_address`, `send` |
| `wallet` | `activate`, `rotate` — WSCA lifecycle; **Axis-A-gated** (act on the owner's behalf) |
| `settings` | `read`, `manage_theming`, `manage_issuer`, `manage_smtp` |
| `relying_parties` | `authorise`, `manage`, `revoke` — Art 5(1) point 10; #27 follow-up slice |
| `audit` | `read` |

Role → permission sketch (the committed map is the source of truth; this is illustrative):

- `admin` → all `members:*`, all `attestations:*`, all `qerds:*`, all `settings:*`, `audit:read`. **Not** `mandates:*`, **not** `wallet:*`.
- `member` → `members:read`, `attestations:read`, `qerds:read`, plus whatever its scope explicitly assigns.
- `attestation_issuer` → `attestations:{read,issue,cancel_offer,revoke}`.
- `qerds_operator` → `qerds:{read,provision_address,send}`.
- `auditor` → `*:read` (every resource's read action), including `audit:read`; nothing else.
- `mandates:*` and `wallet:*` → require a legal representative / full-mandate holder (Axis A), independent of role.

`relying_parties:*` is enumerated so the vocabulary is stable, but the RP-authorisation model itself is #27's follow-up; no role grants it in v1.

## Scopes and validity

**Decision: model scope and validity in the grant and in the permission-check signature from day one; enforce org-wide-only in v1** (open question 2). The structured fields cost almost nothing to carry, and #27's real-time engine needs the seam to exist. What v1 actually *enforces* stays equal to today (org-wide, no window); department/resource-type narrowing and validity windows are the fields #27 turns on.

A grant carries a scope:

- **Org-wide** (default — equals today's behaviour).
- **Department-scoped** — limited to a `department_id`, reusing the existing `departments` table and `Membership.DepartmentID` (`department_store.go`), which are organisational metadata only today.
- **Resource-type-scoped** — limited to one resource domain from the matrix above (e.g. a `qerds_operator` scoped to `qerds` only).

And a validity window: `valid_from` / `valid_until` plus optional constraints (Annex §12(1)(c)), mirroring the columns `wallet_representations` already has. An expired grant is rejected in real time; expiry is #27's sweep-and-reject, the same shape as the invitation-expiry precedent (`accept.go`).

## Assignment & delegation

- Only a **legal representative** (or a full-mandate holder, where the register authority permits) may `mandates:grant`. Administrative-mandate holders (`admin`) assign **functional roles** within their own scope — they cannot mint mandates.
- **No escalation above one's own level; no over-delegation** (Annex §12(3)(b)): a delegate's grant is intersected with what the delegator holds — never a superset of scope, validity or permissions. A delegation whose window exceeds its parent's is clamped to the parent.
- Assignment happens at invite time (`Invitation.Role`, carried onto the membership at accept) and via `UpdateMembership` (`members.go`, `handler.go`). Both extend to carry basis-of-authority, mandate reference, scope and validity alongside the role.
- **Guardrails:** last-legal-representative protection (mirroring the existing last-admin guard `ErrLastAdmin` / `lockAndCountAdmins`); self-demotion rules; conflict-of-roles detection (§12(3)(b)) — a subject cannot simultaneously hold roles the policy declares incompatible.

## Revocation

- Revoke a functional role, revoke a mandate or a single delegation link, downgrade a role, or revoke the membership entirely. The owner can manage and revoke user authorisations (Art 5(1) point 9) and, via the follow-up RP slice, relying-party authorisations (point 10).
- **Immediate and effective-dated.** Immediate revoke sets `revoked_at` (as `wallet_representations` already does); an effective-dated revoke sets a future `valid_until`. Expired mandates auto-expire and are rejected in real time (§12(3)(b)) — this is #27's lifecycle.
- Revoking a mandate **cascades down its delegation chain**: a delegate cannot outlive the authority it was cut from.
- Every assign / change / revoke / delegate writes an **audit event** (who, basis of authority, mandate type, role, scope, on whom) through the existing transactional `audit.Record` seam. `membership.role_changed`, `wallet.representation_claimed` / `_revoked` already exist; the new grant/delegate/mandate events extend the same vocabulary. §12(2) requires binding to verifiable proofs of authorisation — the `source_message_id` link on register-backed representations is the existing precedent; binding granted mandates to proofs is #27's work.

## Enforcement seam

Replace the binary gate with a permission gate, keeping the change localised behind the existing `Authorize` / context seam so #27 can extend it without touching call sites:

```go
// Today (middleware.go):
mux.Handle("POST /orgs/{slug}/members", orgScoped(RequireOrgAdmin(h.invite)))

// Under this model:
mux.Handle("POST /orgs/{slug}/members", orgScoped(RequirePermission("members", "invite")(h.invite)))
```

`Authorize` already resolves the org and stashes the effective role in context (`contextWithRole`). It extends to also stash the **authorisation context** — basis of authority, mandate reference, scope, validity — read from `wallet_representations` + the new grant object. `RequirePermission(resource, action)`:

1. reads the authorisation context (never trusts the request);
2. resolves the caller's role → permission set;
3. checks the permission is granted, the scope covers the target, and the window is currently valid;
4. for `mandates:*` / `wallet:*`, additionally checks Axis A (legal representative / full-mandate holder).

**v1 scope of the check:** role → permission (the compiled map) with org-wide scope and no window — behaviourally equal to today's `RequireOrgAdmin` for `admin`, plus the finer roles. Scope narrowing, validity windows, conflict/over-delegation detection and proof binding are the seams #27 fills; the signature and context shape do not change when it does. `RequireOrgAdmin` becomes `RequirePermission` over the admin permission set and is kept as a thin alias during migration.

## Decisions settled (the open questions)

- **Fixed vs custom roles (v1):** fixed, code-defined. Custom roles deferred.
- **Scopes + validity in v1:** modelled in the grant and the check signature now; enforcement stays org-wide/no-window in v1, narrowing turned on by #27.
- **Permissions as data or code:** code — a compiled `role → permission` map, auditable via git and covered by a table-driven test. DB-backed editable permissions deferred with custom roles.
- **How the legal representative is established / re-verified:** via the KVK register bootstrap (`wallet_representations`, `kind = bestuurder`), sourced from the registration attestation over QERDS (`source_message_id`) — see `wallet-bootstrap.md`. Re-verification rides the attestation's `valid_until` and a re-consult of the register; the exact EU Company Certificate / Annex VI attribute set (Dir (EU) 2025/25) is pinned when implementing acts land (Annex §12(4)).
- **External legal persons as representatives:** modelled as a mandate with `grantee_type = organization`; their sub-delegations are bounded by the parent mandate's scope. Full external-org onboarding deferred.

## Phasing

- **This design (#115):** the vocabulary — roles, permission matrix, scope/validity model, Axis A/B split, lifecycle and enforcement seam. No enforcement engine.
- **#113 (consent & approval):** *who may approve* a disclosure or issuance build reads the roles/mandates fixed here.
- **#27 (real-time enforcement):** the grant → active → revoked/expired lifecycle, scope narrowing, validity windows, conflict/over-delegation detection, and proof binding — filling the seams this design leaves.

## Out of scope / deferred

- The real-time enforcement engine, expiry sweep, and proof binding — #27.
- Relying-party authorisation model (Art 5(1) point 10) — enumerated only; separate slice.
- Custom/DB-backed roles and an admin UI for them.
- External-legal-person onboarding and identification beyond the model shape.
- Cross-wallet mandate/delegation interoperability wire formats — blocked on implementing acts (Annex §12(4)).

## Harvest

- Convention to add/update in `.ai/conventions/`? **none** (no code lands in this PR; the enforcement convention is written when #27 implements `RequirePermission`).
- Feature doc to write/update in `.ai/features/`? **none yet** — this plan becomes the `rbac` feature doc when the model is implemented, per the plans/README Harvest step. Until then it is the design of record for #115, #113 and #27.
