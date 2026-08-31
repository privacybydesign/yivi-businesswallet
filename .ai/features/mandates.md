# Mandates: granted authority, real-time enforcement, revocation

**Status:** first version. The mandate object, its enforcement in `Authorize` / `RequireOrgAdmin`, the
grant → active → revoked/expired lifecycle, and the register screen exist; there is no cryptographic
proof binding.
**Code:** `backend/internal/organization/mandate.go` (model + the pure decisions),
`mandate_store.go` (the store), `mandates.go` (HTTP), `middleware.go` (enforcement),
migration `20260811150000_create_mandates.sql`, `frontend/src/routes/mandate-settings.tsx` +
`frontend/src/lib/mandate.ts` (the screen and its pure logic).
**Design of record:** `.ai/plans/rbac-model.md`. Issues [#27](https://github.com/privacybydesign/yivi-businesswallet/issues/27),
[#210](https://github.com/privacybydesign/yivi-businesswallet/issues/210).

---

## 1. What this is

The EBW Regulation wants an authorisation decision to weigh more than a role. It must weigh the
acting subject's **basis of authority** — the mandate, delegation or power of attorney they act
under — together with that mandate's **scope and validity**, and it must reject expired,
conflicting and over-delegated authorisations *in real time* (Recital 18, Art 3(19), Art 5(1)(j),
Annex §12(1)(c) and §12(3)(b)).

`rbac-model.md` calls that basis **Axis A** and keeps it apart from **Axis B**, the functional role
(`admin` / `member`). The mandate says *whether and within what bounds* you may act; the role says
*what* you may do inside those bounds. Before this slice, only Axis B existed in the authz path, and
only as one admin bit.

Axis A now has two halves:

- **Register-backed** — a `wallet_representations` row, written by the wallet slice from the KVK
  registration attestation. A claimed, unrevoked `bestuurder` is the **legal representative**: the
  root of authority, which cannot be minted from inside the wallet.
- **Granted** — a `mandates` row. The owner, acting through a legal representative, grants a
  **full** or **administrative** mandate to a member; a mandate holder may **delegate** onward,
  forming a chain.

## 2. The mandate

| field | meaning |
|---|---|
| `type` | `full` (act on the owner's behalf generally) or `administrative` (assign roles within the scope). Full strictly contains administrative. |
| `grantor_user_id` / `grantee_user_id` | who gave it and who holds it. The grantee must be a member. |
| `scope` + `scope_department_id` | `organization` (org-wide) or `department` (confined to one department). |
| `parent_mandate_id` | set when the mandate was delegated; the chain is what makes over-delegation checkable and what a revocation cascades down. |
| `valid_from` / `valid_until` | the validity window. `valid_until` NULL is open-ended. |
| `revoked_at` / `revoked_by_user_id` / `revocation_reason` | the revocation. |

**There is no `status` column.** The lifecycle is derived from `revoked_at` and the window against
the clock, at read time (`mandateStatus`):

```
pending  ── valid_from in the future
active   ── in force, not revoked
expired  ── valid_until has passed
revoked  ── revoked_at is set (wins over expired: a mandate revoked before its
            window closed was revoked, and the log should say so)
```

A stored status would only be as fresh as whatever last wrote it, and §12(3)(b) asks for expiry to
be rejected in real time. Nothing sweeps; the predicate is evaluated on every read.

## 3. Enforcement

`Authorize` already resolved the org and the caller's role. It now also resolves their **basis of
authority**, one indexed round trip per org-scoped request (`ResolveAuthority`), and stashes it:

```go
type Authority struct {
	LegalRepresentative bool // claimed, unrevoked, in-window `bestuurder`
	FullMandate         bool // active org-wide mandate of type full
	Mandated            bool // at least one active org-wide mandate, either tier
	Granted             int  // mandates granted here whose window has opened
	PlatformAdmin       bool
}
```

Two gates read it.

**`RequireOrgAdmin`** — role `admin` *and* `!Authority.Withdrawn()`. Authority is withdrawn when the
caller has been granted mandates in this organisation and none is now an active org-wide one: every
grant revoked, expired, or narrowed to a single department. So a revocation takes effect on the
caller's next request without a second write to the membership row, a department-scoped mandate does
not carry org-wide admin, and — because `Granted == 0` is never withdrawn — **an organisation that
has never granted a mandate behaves exactly as before**. The mandate layer is opt-in per
organisation.

Two things it does not withdraw, because neither draws its authority from the mandate register:

- **`PlatformAdmin`** — deployment-level and orthogonal, and an org's register must not lock the
  operator out of its own deployment.
- **`LegalRepresentative`** — the register-backed root. They may grant and revoke through
  `RequireMandateAuthority`, so refusing them `RequireOrgAdmin` would let them write a register they
  cannot read.

`Granted` counts only mandates whose window has **opened**. A mandate that is not in force yet is
neither "never had one" nor "had one and lost it": counting it would mean scheduling a deputy for
next month strips the grantee's admin access today and hands it back when the mandate starts. The
narrowing begins when the mandate does.

**`RequireMandateAuthority`** — gates granting and revoking on Axis A alone: legal representative, or
an active org-wide full mandate. No functional role reaches it, so an `admin` cannot mint itself a
mandate. This is the rule that keeps `admin` an *administrative* mandate rather than an owner.

The middleware is the cheap gate; the store is the decision of record. `GrantMandate` and
`RevokeMandate` re-derive the caller's authority inside their own transaction rather than trusting
the context, so a caller who reaches the store another way gets the same answer.

## 4. Delegation, and the over-delegation rule

A grantor who is not a legal representative is delegating: the grant is cut from a mandate they
hold. If they name no parent, their own active org-wide full mandate is used.

`clampToParent` (pure, unit-tested) enforces Annex §12(3)(b):

- the parent must be **active** — nothing is cut from a revoked, expired or pending mandate;
- the tier may not **exceed** the parent's (`full` under `administrative` is rejected);
- the scope may not be **widened** — a department-scoped parent can only produce a mandate in that
  same department;
- the window is **clamped**, not rejected: `valid_from` up to the parent's, `valid_until` down to
  it, and an open-ended delegation inherits the parent's end. A delegation cannot outlive the
  authority it was cut from. If the clamp leaves an empty window, there was nothing to cut.

Tier and scope are errors rather than silent rewrites on purpose: someone who asked for a full
mandate should not be told they got one.

**A test cannot build an already-expired *delegation* under a parent that starts now.**
`clampToParent` lifts the child's `valid_from` up to the parent's, so a window like `[-2h, -1h)`
becomes `[now, -1h)` and is rejected as `ErrOverDelegation` before the insert — the store is right
and the test is wrong. Back-date the parent (`ValidFrom: now-3h`) and the child's closed window
survives the clamp. Needed for anything covering the revocation cascade's treatment of mandates
that ended on their own.

## 5. Revocation

`POST /orgs/{slug}/mandates/{id}/revoke`, by a legal representative (any mandate) or the mandate's
own grantor (only what they gave).

Only a mandate that still has a life ahead of it can be revoked — `pending` or `active`. One that is
already `revoked` or `expired` is a 409: it ended on its own date, and stamping `revoked_at = now()`
on it would relabel a historical mandate in the register this layer exists to keep honest.

- **Immediate** (no body): `revoked_at = now()`.
- **Effective-dated** (`effectiveAt` in the future): closes the validity window on that date, so the
  mandate stays active until then and expires on its own.

Either way the revocation **cascades down the delegation chain** — a delegate cannot outlive the
authority it was cut from. A descendant whose window has not opened by the effective date cannot
have it trimmed (`valid_until` would land at or before `valid_from`), so it is revoked outright; it
could never have become effective anyway. An already expired descendant is left out of the cascade
for the same reason the target has to be pending or active.

Every mandate the revocation reaches gets its own `mandate.revoked` audit event, carrying
`cascadedFrom` when it was reached through the chain, so the whole cascade is readable from the log.
Grants write `mandate.granted`. Both record the `basis` the mandate stands on, in the standard
`{before, after}` envelope with readable values.

The envelope is chosen **per row**, not per request: a row the cascade revoked outright is a
`Deleted` (detail under `before`), a row whose window was trimmed to a future date is an `Updated`
carrying `effectiveAt`. Branching on the request's `effectiveAt` alone would log a row that is gone
as one that is still active until that date.

## 6. API

| route | gate |
|---|---|
| `GET /orgs/{slug}/mandates` | `RequireOrgAdmin` — the whole register, ended mandates included; dropping them would hide the revocations |
| `GET /orgs/{slug}/mandates/authority` | `RequireOrgAdmin` — the caller's own basis, for the screen to offer only the actions they can use |
| `POST /orgs/{slug}/mandates` | `RequireMandateAuthority` |
| `POST /orgs/{slug}/mandates/{id}/revoke` | `RequireMandateAuthority` |

`Authority` itself never reaches the client. The authority route serves a flattened read of it —
`mayGrant` (the `RequireMandateAuthority` gate verbatim), plus `legalRepresentative`, `fullMandate`
and `jointAuthority` so the screen can say *why* it is withholding an action. `jointAuthority` is the
one field not derived from `Authority`: it reads `wallet_representations.authority` for the caller,
which `ResolveAuthority` deliberately ignores (§7).

## 6a. The register screen

`Mandates` under org settings (`?tab=mandates`). It reads the register and the authority route, and
otherwise holds no state the backend does not.

- **Status is not recomputed.** `Mandate.Status` is derived server-side on every read, so the screen
  renders the field. There is no second copy of the predicate to drift.
- **Lineage** (`mandateLineage`) nests each mandate under the one it was cut from, depth-first. Every
  mandate comes out exactly once: a row whose `parentMandateId` is absent from the list is shown as a
  root rather than dropped, because the register is the audit surface and a vanished row would hide a
  grant.
- **The cascade warning** (`mandateCascade`) names the descendants a revocation would reach. It walks
  the whole subtree and then drops the revoked and expired ones, which is exactly what the recursive
  CTE + `WHERE revoked_at IS NULL AND (valid_until IS NULL OR valid_until > now())` does — so it
  descends *past* an ended descendant to the live ones under it.
- **The grant flow is withheld from a jointly registered director** unless they also hold a full
  mandate (`mandateGrantAvailability`). The backend would accept their grant, because it does not read
  the column yet; offering the flow would record a grant no single director could make. A mandate of
  their own is a basis that does not depend on the registration, so it lifts the hold.
- **An admin whose own mandate lapsed** fails `RequireOrgAdmin` and the register 403s with
  `mandate_withdrawn`. The screen names that specifically: `accessMessage`'s "you are not a member of
  this organization" is wrong for someone who is a member and whose authority ran out.

## 7. Deliberately not here

- **Cryptographic proof binding of audit events** (Annex §12(2)(c)) — the events are written and
  timestamped, but not yet bound to a verifiable proof of authorisation. `source_message_id` on
  register-backed representations is the existing precedent to follow.
- **`RequirePermission(resource, action)`** — the finer permission matrix of
  [#115](https://github.com/privacybydesign/yivi-businesswallet/issues/115) is not in `main` (see the
  note in `.ai/conventions/BACKEND.md`), so this slice extends `RequireOrgAdmin` instead. Nothing
  here has to change when it lands: the gates read `Authority` out of context, not a role string.
- **Resource-domain scope.** Scope is org-wide or department; narrowing to one resource domain
  belongs with `RequirePermission`. The grant form says so under its scope field rather than offering
  a control that does nothing.
- **External legal persons as grantees** (Art 3(18) allows one). The grantee is a user; an
  `accounting firm holds a mandate` needs onboarding that does not exist yet.
- **Joint authority.** `legalRepresentativeExists` reads `kind = 'bestuurder'` and ignores
  `wallet_representations.authority`, so a director registered `jointly` — who under Dutch law
  cannot bind the company alone — is treated here as a full legal representative. Honouring the
  column properly means co-signing: two directors together making one root grant, with a pending
  grant waiting for the second. That is a slice of its own. Refusing them outright instead would
  leave a jointly-managed company with no way to grant a mandate at all, which is worse than the
  gap. This is the first code in the repo to decide anything from that row, so nothing regresses —
  but it must be closed before the layer carries a legal claim. The register screen does not offer
  such a director a grant flow (§6a), so the gap is no longer reachable from the UI; the API still
  honours them, and closing it there is the co-signing slice.
- **Conflict-of-roles detection**, relying-party authorisations (Art 5(1)(k)), and cross-Member-State
  interoperability — separate slices per #27.
