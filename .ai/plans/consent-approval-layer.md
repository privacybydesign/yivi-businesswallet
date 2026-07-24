# Consent & approval layer: who may say yes on behalf of the org

Status: **proposed** (design only — not yet implemented). Issue [#113](https://github.com/privacybydesign/yivi-businesswallet/issues/113), sub-issue of the mandate & role-based authorisation epic [#27](https://github.com/privacybydesign/yivi-businesswallet/issues/27). Builds on the vocabulary fixed by the RBAC design ([#115](https://github.com/privacybydesign/yivi-businesswallet/issues/115), [`rbac-model.md`](./rbac-model.md)).

## Why

The business wallet holds and acts on credentials for a **legal person**, not a human. The personal-wallet consent model — the holder *is* the human, consent *is* a tap on their device — does not carry over: "the org" cannot tap approve. Yet every state-changing wallet action needs a yes:

- **Disclosing** attributes to an external verifier (OpenID4VP presenter, [#112](https://github.com/privacybydesign/yivi-businesswallet/issues/112)).
- **Accepting** a credential into the org's held holdings (OID4VCI-over-QERDS issuance, [#32](https://github.com/privacybydesign/yivi-businesswallet/issues/32), [#105](https://github.com/privacybydesign/yivi-businesswallet/issues/105)).

Neither the presenter (`internal/presentation`, #112) nor the holder-acceptance path (#32) is built yet, and both are converging on the same missing question: *who is allowed to say yes, and on what basis?* That question is cross-cutting — one governance layer serves both directions — so it is worth fixing before either flow grows its own ad-hoc gate.

This document specifies that layer. It does **not** build the disclosure or acceptance flows (those are #112 / #32), and it does **not** build the real-time mandate-enforcement engine (#27). It defines *who may approve*, the four authorisation modes, the queue and policy models, and the enforcement seam — reusing the roles, permission matrix and Axis A/B split fixed by #115.

## Not in tension with the self-managing wallet

The WSCA holder-binding direction ([`wsca-holder-binding.md`](../features/wsca-holder-binding.md), COM(2025) 838) is about the **key layer**: the wallet signs without a human unlocking a device per operation. This layer is the **authorisation/business-decision layer**: *should we disclose or accept this at all?* They compose:

> A human — or a human-authored policy — approves the business decision; the wallet then autonomously performs the cryptography.

"Self-managing" means no human babysits the keys, not that actions happen ungoverned.

## The four authorisation modes

A single spectrum, applied uniformly to presentation and issuance. Each action resolves to exactly one mode at decision time.

1. **Human-initiated** — an operator with the right permission *starts* the session (login, KYB onboarding, requesting a credential the org needs). Initiation is consent; there is no second gate. This is today's only shape (`/enroll`, login).
2. **Human-approved (queued)** — an inbound, unsolicited request lands in an **approval queue**. A member with the decide permission reviews **what is disclosed / what is accepted, and to/from whom**, then approves or declines. The approver need not be the initiator; there may be no initiator at all.
3. **Policy-auto-approved** — an admin pre-authors a **policy** ("attribute X to verifiers matching Y ⇒ auto-approve"; "auto-accept renewals from trusted issuer Z"). The wallet responds autonomously. The human approval happened once, at policy-authoring time — this is where "no human in the loop" legitimately lives.
4. **Four-eyes / dual control** — a high-value action requires **two** distinct approvers before it proceeds. Layered on mode 2's queue; a policy or a resource threshold marks an action as dual-control.

Every path — including policy-auto — writes an audit event: *who approved (or which policy), what, to/from whom, under which policy*.

## Symmetry: both directions, one layer

The queue holds two kinds of pending item; the model, permissions, policy engine and audit are shared.

| | Presentation (#112) | Issuance / QERDS (#32, #105) |
|---|---|---|
| Inbound, unsolicited | approve/decline **what we disclose** | approve/decline **what we accept/hold** |
| Human-initiated | login / onboarding | request a credential we need |
| Policy-auto | routine re-proof | auto-accept a trusted issuer's renewals |

Delivery and acceptance are **not** in conflict (#105): automatic delivery lands the offer in the wallet's inbound store; **acceptance into the org's held credentials is the gated step here**. Symmetrically, an inbound presentation *request* is received automatically; **disclosure is the gated step**.

## The permissions this adds

The RBAC matrix (#115) enumerates `{resource}:{action}` permissions as a compiled, table-driven map. This layer extends that vocabulary with one new resource for the decision surface and one for policy authoring:

| resource | actions |
|---|---|
| `approvals` | `read` (see the queue), `decide` (approve/decline a pending item), `decide_dual` (act as the second approver on a four-eyes item) |
| `policies` | `read`, `author` (create/edit auto-approve/auto-decline rules), `revoke` |

Role → permission, extending the #115 sketch (the committed map stays the source of truth):

- `admin` (administrative mandate) → `approvals:{read,decide,decide_dual}`, `policies:{read,author,revoke}`.
- `attestation_issuer` → `approvals:{read,decide}` for **issuance** items only, `policies:read`. Scoped to the `attestations` domain via the existing resource-type scope (#115 "Scopes and validity").
- `qerds_operator` → `approvals:{read,decide}` for items whose disclosure/acceptance runs over QERDS, `policies:read`.
- `auditor` → `approvals:read`, `policies:read`. Read-only, never a decider (mirrors its `*:read`-only rule in #115).
- `member` → nothing by default; a scope may grant `approvals:read`.

**Authoring a policy is not the same authority as making one decision.** `policies:author` is admin-only because a policy pre-approves a whole class of future actions — it is closer to an administrative-mandate act than to a single yes. A `decide` holder can approve the item in front of them; only `author` can write the rule that approves the class. This keeps mode 3's "the human approval happened once" honest: the once is an admin authoring under their administrative mandate.

`decide_dual` is a distinct permission, not a flag on `decide`, so a role can be allowed to *originate* an approval without being allowed to *complete* a four-eyes one alone — and so the four-eyes constraint ("two **distinct** subjects") is expressible as "the second decider holds `decide_dual` and is not the first decider".

None of these are Axis-A-gated. Approving a disclosure or acceptance is a functional act inside the administrative-mandate space; it is not `mandates:*` or `wallet:*` (those stay gated on the legal representative / full-mandate holder per #115). Granting the *authority to approve* is role assignment, which already flows through invite / `UpdateMembership`.

## The approval queue

**There is already a queued-human-approval precedent in the org slice: identity review.** An invitation-accept whose disclosed name mismatches lands a `pending` row in `identity_reviews`; an admin resolves it approve/reject; every transition audits through the same `InTx` + `audit.Record` seam (`internal/organization/identity_review_store.go`). Only `pending` and `rejected` are persisted `ReviewState` values — an approval creates the membership and cascade-deletes the review row, so "approved" survives as the `user.identity_review_approved` audit action, not as a stored status (actions `user.identity_review_{required,approved,rejected}`). The approval queue is the same shape, generalised to two payload kinds — and, unlike the identity-review precedent, it will legitimately persist an `approved`/`declined` status rather than deleting the row, since the decided item is itself the record to audit against.

A pending item carries enough to review the decision **and** to reconstruct it for audit:

| field | meaning |
|---|---|
| `kind` | `presentation` (disclose to a verifier) or `issuance` (accept a held credential) |
| `counterparty` | the verifier (presentation) or issuer (issuance) identity, as far as the protocol authenticates it |
| `requested` | the attribute set requested/offered — the reviewable payload; drives **selective, attribute-level** approval (Annex §12(2): selective credential visibility) |
| `status` | `pending` → `approved` / `declined` / `expired` / `superseded_by_policy` |
| `mode` | which of the four modes resolved this item |
| `decided_by`, `decided_at` | first approver |
| `dual_decided_by`, `dual_decided_at` | second approver, four-eyes only; `NULL` otherwise |
| `policy_id` | the policy that auto-decided it, when `mode = policy-auto` |
| `expires_at` | the request's own validity; an un-actioned item expires rather than lingering (invitation-expiry precedent, `accept.go`) |

Attribute-level review means an approver may approve a **subset** of what was requested — the decision records exactly which attributes were disclosed/accepted, not just a yes. This is the fine-grained, auditable outcome Annex §12(2) requires, and it is the presentation/issuance flow's job to honour the subset when it acts.

State transitions run in one transaction with their audit write, exactly as identity-review and membership already do — no decision without its audit row.

## The policy engine

A policy is admin-authored data (mode 3). It is evaluated when a pending item is created; a match short-circuits the queue and auto-decides.

- **Selector** — `kind` + counterparty match (issuer/verifier identity or a pattern over it) + attribute-set constraints (which attributes, optional value constraints).
- **Effect** — `auto_approve` (optionally narrowed to a subset of the requested attributes) or `auto_decline`.
- **Provenance** — grantor (the authoring admin), `created_at`, validity window (reusing #115's `valid_from` / `valid_until` shape), `revoked_at`. A policy is itself a revocable, audited authorisation.

Evaluation order and safety:

- **First matching policy wins**; author order is explicit, not incidental.
- **No match ⇒ the item stays `pending`** and falls to mode 2. Absence of a policy never means auto-yes.
- A **four-eyes marker** (from a policy or a resource threshold) always beats `auto_approve`: a high-value action a policy would approve is instead queued for two humans. Dual-control is a floor, not something a policy can waive.
- Authoring, editing and revoking a policy each audit. Policy authorship is the durable record of a mode-3 human decision, so it must be as auditable as an individual approval.

Whether policy matching itself is compiled code or DB-backed data mirrors #115's resolved question for permissions: **start with the structured model; the matcher is code first** (auditable via git, covered by a table-driven test), with DB-backed admin-editable policies as the layered follow-up once the selector vocabulary has settled.

## Audit integration

Every decision writes through the existing transactional `audit.Record(ctx, q, action, target, metadata)` seam (`internal/audit/audit.go`). New actions and a target extend the current vocabulary (which already carries `membership.*`, `wallet.representation_*`, `attestation.*`, and the `user.identity_review_*` family):

- Actions (illustrative): `approval.requested`, `approval.approved`, `approval.declined`, `approval.expired`, `approval.auto_approved`, `approval.auto_declined`, `policy.created`, `policy.updated`, `policy.revoked`.
- Target: `approval_request` (and `policy`).

Metadata records the compliance facts Annex §12(2) asks for: approver(s), decision, the **disclosed/accepted attribute subset**, counterparty, and the policy applied. §12(2) also requires events be **bound to cryptographically verifiable proofs of authorisation** — that binding is #27's work (the `source_message_id` link on register-backed representations is the existing precedent, per #115); this layer produces the events and the fields, and leaves the proof-binding seam for #27 to fill.

> **Frontend contract:** every new audit action needs an `auditLog.actions.*` translation and a `case` in `frontend/src/lib/audit-event.ts`, or `audit-event.test.ts` fails (it parses the constants out of `audit.go`). Adding the actions above lands those i18n keys in the same change that adds them (see AGENTS.md).

## Enforcement seam

The layer plugs into the `RequirePermission(resource, action)` gate #115 defines, and sits between "a request arrived" and "the wallet acts":

```
inbound request (presentation | issuance)
        │
        ▼
  policy evaluation ─── match ──▶ auto-decide ──▶ audit ──▶ act (or not)
        │ no match
        ▼
  enqueue as pending  ──▶  approver hits POST /orgs/{slug}/approvals/{id}
                                    guarded by RequirePermission("approvals","decide")
                                    (+ "decide_dual" and distinct-subject check for four-eyes)
                                    ──▶ audit ──▶ act on the approved subset
```

- The **decide endpoints** are `RequirePermission`-gated like every other privileged route (#115), reading the caller's role/scope from the `Authorize` context — never trusting the request body for who the caller is.
- **Attribute-scope enforcement** (an `attestation_issuer` decides only issuance items; a department-scoped role decides only its department's items) rides #115's scope model; v1 enforces org-wide, the fields are carried from day one.
- The **acting step** (perform the disclosure / accept into holdings) belongs to #112 / #32; this layer hands them an approved decision + the exact attribute subset and the audit trail, and refuses to let them act without one.

## Decisions settled

- **Who may approve:** functional-role permissions (`approvals:*`), not Axis A. Approving is an administrative-mandate act, not a mandate grant.
- **Who may author policy:** admin only (`policies:author`) — a policy pre-approves a class of actions.
- **Four-eyes as a permission:** `decide_dual` is distinct from `decide`, and dual-control is a floor a policy cannot waive.
- **Queue model:** generalise the `identity_reviews` pending → rejected (approve deletes the row) + audit precedent to two payload kinds, but persist the decided status here; attribute-level (subset) decisions.
- **Policy matcher:** structured model now; matcher is code-first (table-driven test), DB-backed editable policies deferred, mirroring #115's permissions-as-code decision.
- **Scope/validity:** reuse #115's scope + `valid_from`/`valid_until` fields on grants and policies; enforce org-wide in v1, narrowing is #27.

## Open questions

- Which resources/actions default to **four-eyes** — a fixed high-value list (e.g. disclosing identity attributes, accepting a QERDS-delivered credential above a value threshold), or admin-configured per org?
- Do inbound presentation *requests* even reach us un-initiated before #112 lands a presenter, or is the queue issuance-only in the first slice? (The model carries both; the first *implemented* kind may be `issuance`.)
- Should a declined item be **re-openable** by the counterparty (a new request) or is decline terminal for that request id? (Identity-review treats rejection as sticky — a re-accept reports rejected, not a fresh pending. The same stickiness likely fits.)
- Attribute value-constraints in a policy selector for v1, or attribute-set matching only first?

## Phasing

- **This design (#113):** the vocabulary — four modes, `approvals`/`policies` permissions, the queue model, the policy-engine shape, audit actions, and the enforcement seam. No queue, no engine, no UI built.
- **#112 / #32 (the flows):** the presentation presenter and the holder-acceptance path call this seam — enqueue on inbound, act only on an approved decision + subset.
- **#27 (real-time enforcement):** scope narrowing, validity-window enforcement, conflict/over-delegation detection, and binding approval events to verifiable proofs of authorisation.

## Out of scope / deferred

- The approval queue store, the policy matcher, and the approval-inbox / policy-management UI — implemented when the first gated flow (#112 or #32) lands, against this model.
- Binding approval events to cryptographic proofs of authorisation — #27.
- DB-backed, admin-editable policies and their management UI — after the code-first matcher and selector vocabulary settle.
- Relying-party authorisation management (Art 5(1) point 10) — enumerated in #115, a separate slice.

## Harvest

- Convention to add/update in `.ai/conventions/`? **none** (no code lands in this PR; the enqueue-then-decide convention is written when #112/#32 implement the queue against this model).
- Feature doc to write/update in `.ai/features/`? **none yet** — this plan is the design of record for #113 and the consent seam #112/#32/#27 build on; it becomes a `consent-approval` feature doc when the queue is implemented, per the plans/README Harvest step.
