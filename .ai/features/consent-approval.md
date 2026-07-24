# Consent & approval — the approval queue and policy engine

**Status:** v1 implemented (queue store + code-first policy engine + audit). The HTTP surface, the
approval-inbox / policy UI, and the wiring from the presentation (#112) and holder-acceptance (#32)
flows are those flows' work, against this model. Real-time scope/validity enforcement and proof
binding are #27.
**Regulation:** COM(2025) 838 Annex §12(1)(2); Recital 18.
**Design of record:** `.ai/plans/consent-approval-layer.md` (issue #113, epic #27). Read it for the
*why*; this doc is the *what shipped*. Builds on the RBAC vocabulary (`rbac.md`, #115).

---

## 1. What this is

The business wallet acts for a legal person, not a human, so "the org taps approve" does not exist.
Every state-changing wallet action still needs a yes: **disclosing** attributes to a verifier (#112)
and **accepting** a credential into the org's holdings (#32). This slice is the one governance layer
both directions share — the approval queue that holds pending items, and the policy engine that can
auto-decide them — plus the permissions and audit that make each decision accountable.

It is the *model + mechanism*. It does not build the disclosure/acceptance flows, their HTTP
endpoints, or a UI; those call this store when they land. The `approvals`/`policies` permissions it
is gated on are already live in the RBAC matrix.

## 2. The permissions (`internal/organization/permissions.go`)

Two resources extend the matrix; none is Axis-A-gated (approving is a functional act inside the
administrative-mandate space, not a mandate grant):

| resource | actions | who holds them |
|---|---|---|
| `approvals` | `read`, `decide`, `decide_dual` | admin: all three · attestation_issuer / qerds_operator: `read`+`decide` · auditor: `read` only |
| `policies` | `read`, `author`, `revoke` | admin: all three · the decider roles + auditor: `read` only |

Two guardrails are pinned by `permissions_test.go`:

- **`policies:author` is admin-only.** A policy pre-approves a whole class of future actions — closer
  to an administrative-mandate act than to a single yes.
- **`approvals:decide_dual` is admin-only and distinct from `decide`.** A role can be allowed to
  *originate* an approval without being allowed to *complete* a four-eyes one alone; the four-eyes
  "two distinct subjects" rule is then expressible as "the second decider holds `decide_dual` and is
  not the first decider".

## 3. The four authorisation modes

Each queued item resolves to one mode (`internal/consent/consent.go`):

- **human_initiated** — reserved; initiation is itself consent, so these never enter the queue.
- **human_approved** — queued for one decider.
- **policy_auto** — a matching policy decided it at enqueue time.
- **four_eyes** — queued, needs two distinct approvers.

## 4. The approval queue (`internal/consent`, `approval_requests`)

Generalises the `identity_reviews` *pending → decided + audit* precedent to two payload kinds
(`presentation` / `issuance`). Unlike identity-review it **persists** the decided status: the decided
item is itself the record audited against. Each item carries the reviewable payload (`requested`
attribute set), counterparty, mode, expiry, and — once decided — the **approved attribute subset**,
so an approver can approve a subset of what was requested (Annex §12(2)). Store methods:

- `Enqueue` — records an inbound request, evaluating policies first (see below).
- `Decide` — resolves a `human_approved` item, or records the **first** approval of a `four_eyes` one
  (which stays pending); a decline is terminal in either mode.
- `DecideDual` — completes a `four_eyes` item as the distinct second approver; the first approver's
  subset stands.
- `ListPending`, `SweepExpired` (marks overdue items `expired` + audits each; #27 owns the schedule).

An approved subset must be a non-empty subset of the requested attributes.

## 5. The policy engine (`internal/consent/policy.go`, `policies`)

Admin-authored, code-first matcher (mirrors #115's permissions-as-code decision; DB-editable policies
are deferred). `Match` is pure and first-match-wins:

- **Selector** — `kind` + counterparty (`*` any / trailing-`*` prefix / exact) + a required-attribute
  set the request must contain.
- **Effect** — `auto_approve` (optionally narrowed to a subset of the requested attributes) or
  `auto_decline`.
- **Provenance** — authoring admin (`created_by`), timestamps, a validity window, `revoked_at`.

Safety rules, enforced by `Enqueue` + integration tests:

- **First matching policy wins**, ordered by the admin's explicit `priority` then age.
- **No match ⇒ pending.** Absence of a policy never means auto-yes.
- **A four-eyes marker beats `auto_approve`** (from the policy's `four_eyes` flag or the caller's
  `ForceDualControl` threshold): the item is queued for two humans, never auto-approved. Dual control
  is a floor a policy cannot waive. (`auto_decline` is safe to automate regardless — declining never
  needs two subjects.)
- **Revocation is immediate** — a revoked policy is dropped by the matcher's query at once. Validity
  *windows* are carried but not yet enforced (org-wide/no-window in v1, mirroring the RBAC seam; #27
  turns them on).

## 6. Audit integration

Every state change writes through the transactional `audit.Record` seam in the same transaction as
the change — no decision without its audit row. New actions in `audit.go`: `approval.requested`,
`approval.approved`, `approval.declined`, `approval.expired`, `approval.auto_approved`,
`approval.auto_declined`, and `policy.{created,updated,revoked}`; new targets `approval_request` and
`policy`. Each gains an `auditLog.actions.*` / `auditLog.targets.*` translation (en + nl) and a `case`
in `frontend/src/lib/audit-event.ts`, or `audit-event.test.ts` fails. A four-eyes item's *first*
approval is not itself a decision, so the completed decision (recording both approvers) is the audited
event.

Binding decisions to cryptographically verifiable proofs of authorisation (Annex §12(2)) is #27.

## 7. Reserved-but-unproduced vocabulary

Kept for model completeness (the `relying_parties` enumerated-ahead-of-flow precedent): the
`human_initiated` mode and the `superseded_by_policy` status. The latter awaits the on-author
re-evaluation follow-up (policies are evaluated only at enqueue time in v1).
