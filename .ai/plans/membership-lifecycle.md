# Membership lifecycle + audit log

Status: **proposed** (plan only — not yet implemented).

## Why

Today a membership is born `active` in one synchronous `InviteMember` call (`internal/organization/service.go`). An org admin can type *any* email and that person is immediately a member — no consent, no acceptance, no expiry, no revoke-before-accept, and (because the response/member-list joins the global user profile) the admin learns the real name of anyone who already exists in the deployment.

`regulation/FEATURE_LIST.md:64` requires multi-user authorisation to be **verifiable, auditable, revocable, traceable**, with **expired authorisations automatically detected**; line 37 wants **a log of all transactions**; line 117 requires **GDPR data-minimisation**. So a consented, audited, revocable membership lifecycle is in scope.

Two intertwined parts: a **generic audit log** (the load-bearing implementation decision — first) and the **membership lifecycle** (invite → verified consent → active) that is its first consumer.

---

## Part 1 — Audit log

Covers **almost all state-changing actions** (invite/accept/revoke/role-change, department CRUD, org update, identity changes). To be compliance-grade it must be **atomic with the mutation**: no mutation without its audit row, no audit row without the mutation. The audit write shares the mutation's transaction.

### The recording seam — `InTx` + one-line `audit.Record`

Resolves the central tension (same-tx atomicity vs. threading a transaction through every store method by hand) with one helper that every audited mutation runs inside:

```go
err := db.InTx(ctx, func(tx Querier) error {
    // ... the data mutation, using tx ...
    return audit.Record(ctx, tx, audit.MembershipRevoked, target, meta)
})
```

- `database.InTx` owns `BEGIN`/`COMMIT`/`ROLLBACK`. Mutation and audit insert share `tx` → commit or roll back together. No hand-rolled transaction code at the call site.
- `Querier` is an interface (`Exec`/`Query`/`QueryRow`) satisfied by both `*pgxpool.Pool` and `pgx.Tx`; mutating store methods accept it.
- `audit.Record` pulls **actor** (`auth.UserFromContext`) and **request_id** (logging context) out of `ctx`; the call site passes only domain facts (action, target, metadata). The cross-cutting fields can't be forgotten because the caller never supplies them.

Adding audit to a new action = run the mutation in `InTx` and add one `audit.Record` line.

> **Convention note.** This consciously *extends* AGENTS.md's pure-CRUD rule: a pure-CRUD store method now owns a transaction and imports `audit`. Justified by the same-tx atomicity requirement — auditing is a uniform cross-cutting concern, not cross-store orchestration, so a helper fits better than a per-action service layer. **When implemented, update `.ai/conventions/BACKEND.md` (and AGENTS.md's hierarchy note) to codify the exception** — follow the conventions, change them deliberately.

### Rejected alternatives

- **Postgres triggers / CDC** — automatic but the DB doesn't know the *actor* or *intent* (would diff raw columns, not record domain actions) without injecting context via `SET LOCAL` per tx, its own threading problem.
- **Service per audited action** — keeps stores audit-free, but turns "service only when orchestrating 2+ stores" into "service everywhere," adds passthrough boilerplate, and still needs `InTx` + `Querier` underneath.
- **Fire-and-forget async** — a crash between commit and log yields a mutation with no audit row. Breaks atomicity.

### Optional, at two levels

1. **Per action (incremental rollout):** a mutation with no `audit.Record` call emits no event — audit each action as you touch it, nothing breaks meanwhile.
2. **System level (pluggable sink):** `audit.Record` goes through a `Recorder` interface — `dbRecorder` (default, inserts on the tx), `nopRecorder` (disable via config), `outboxRecorder` (future external sinks). Swapping the sink touches no call site.

### Scalability

- **Lean writes:** append-only inserts are cheap; index only what's queried — `(organization_id, occurred_at)`, `(actor_user_id, occurred_at)`. JSONB `metadata` stays unindexed unless a query needs it.
- **Batching:** when one tx emits several events, buffer and flush as a single multi-row INSERT inside `InTx`.
- **Growth / retention (main lever):** time-partition `audit_events` by `occurred_at` (declarative monthly partitions). Time-bounded queries prune partitions; retention is a cheap `DETACH`/`DROP`/archive instead of mass `DELETE`. Deferred until volume warrants, but the schema is partition-ready from day one.
- **External sinks at scale:** the transactional-outbox variant writes the event to an outbox table in the same tx and ships asynchronously — behind the same `Recorder` interface, so the write path never blocks on a slow external system.

### `audit_events` table (new `internal/audit/` slice)

| column | notes |
|---|---|
| `id` | `UUID PK` |
| `occurred_at` | `TIMESTAMPTZ NOT NULL DEFAULT now()` — partition key |
| `actor_user_id` | `UUID NULL` — FK to `users`; null only for system actions (e.g. expiry sweep). Users are pseudonymised in place on purge, never hard-deleted, so this stays resolvable |
| `organization_id` | `UUID NULL` — null = platform-level action |
| `action` | `TEXT` — stable namespaced constants in the `audit` package (below) |
| `target_type`, `target_id` | affected entity (`membership` → `target_id = user_id`, org via `organization_id`) |
| `metadata` | `JSONB NOT NULL DEFAULT '{}'` — predictable camelCase keys (matching the API), e.g. `{oldRole, newRole}` |
| `request_id` | `TEXT NULL` — correlate with app logs |

**Must NOT cascade-delete.** The GDPR purge (below) pseudonymises the `users` row in place rather than deleting it, so `actor_user_id` stays a stable FK and the trail is never orphaned — no snapshot needed; actor identity resolves by join (to the pseudonymised tombstone post-purge). `organization_id` may be `ON DELETE SET NULL` (org names aren't personal data).

Use **stable, namespaced action constants and predictable `metadata` keys** — membership-flow metrics may later aggregate over these events, so don't let event shapes drift.

**Scope boundary:** "almost all actions" = **state-changing mutations**. Read/access logging is a separate, larger concern — flagged, not built here.

### Action vocabulary (initial)

- `membership.invited`, `.invite_resent`, `.accepted`, `.declined`, `.revoked`, `.role_changed`, `.expired` — all keyed to the (user, org) relationship (`target_type='membership'`), regardless of which table currently holds the row.
- `user.identity_changed`, `.identity_review_required`, `.identity_review_resolved`, `.purged`
- `department.created`, `.updated`, `.deleted`
- `organization.updated`

---

## Part 2 — Lifecycle model: separate `invitations` and `memberships`

A pending invite and an active membership have different lifecycles: the invite carries **provisional, admin-asserted, unverified, leak-sensitive** data that is **discarded on accept** once the verified credential supersedes it; the membership is the **real grant** (role/title/dept). So they live in two tables:

- **`invitations`** — exists only while pending. Holds the token, expiry, inviter, the *intended* membership attributes (role/title/dept), and the *asserted* identity (name).
- **`memberships`** — exists only when **active**. Unchanged from today's schema (it already models exactly an active membership).

```
   invite (admin)            accept (invitee, verified)
        │                            │
        ▼                            ▼
  ┌─────────────┐   accept    ┌──────────────┐  revoke / leave
  │ invitations │ ──────────▶ │ memberships  │ ─────────────▶ (deleted)
  │  (pending)  │             │   (active)   │
  └─────────────┘             └──────────────┘
        │
   decline / revoke / expire ──▶ (deleted)
```

**Why two tables (vs. a `status` column on `memberships`):**
- **Authz is correct by construction.** `Authorize`, `ListForUser`, and `lockAndCountAdmins` query `memberships`, which only ever holds active rows — no `status='active'` filter to remember, so a pending invite can never silently grant access or count as an admin. A whole class of "forgot the filter" bugs disappears.
- **Extensible.** New invite-time fields go on `invitations`, never polluting the live-relationship table.
- **Leak-prevention is structural.** Pending rows are serialized from `invitations`, a table with no profile columns to leak.
- **Hygiene.** Accept consumes the invitation, discarding the admin's unverified assertions once verified data exists.

**Hard-delete everywhere.** Decline/revoke/expire **delete** the row; re-invite is a fresh `invitations` insert. State is never reconstructed from events — `audit_events` holds the history; the live tables hold only live rows.

**Invitations are immutable except `resend`** (rotate token + bump `expires_at`; no content change). Any content change — name, role, dept, email — is **revoke + re-invite**: the invited name is an identity *assertion*, so re-asserting it should be a discrete revoke→invite pair in the trail, and revoke kills the stale link while re-invite issues a fresh token/expiry/email.

### Dates

With model C the lifecycle dates fall out of the two tables — no `status` or `accepted_at` column needed:

| date | lives on | meaning |
|---|---|---|
| `invitations.created_at` | invitation | invited-at |
| `invitations.expires_at` | invitation | forward-looking deadline; resend bumps it (cannot be derived once it can move) |
| `memberships.created_at` | membership | "member since" — the row only exists once accepted, so its creation *is* acceptance |

Everything historical/multi-valued (revoked/declined/expired times, resend and role-change history, all time-series stats — acceptance rate, median time-to-accept = `memberships.created_at − invitations.created_at`, invites-over-time) is **derived from `audit_events`**. Invariant: nothing the live system needs for correctness lives only in the (prunable) log.

### `invitations` schema (new)

| column | notes |
|---|---|
| `id` | `UUID PK` |
| `user_id` | `UUID REFERENCES users(id) ON DELETE CASCADE` |
| `organization_id` | `UUID REFERENCES organizations(id) ON DELETE CASCADE` |
| `email` | `TEXT` — the invited address (admin-supplied); kept here so the pending list never joins `users` |
| `invited_by` | `UUID` — inviter, for the pending-list display |
| `role` | intended membership role (`admin`/`member`) |
| `job_title`, `department_id` | intended membership attributes; composite FK `(department_id, organization_id)` as on `memberships` |
| `invited_given_names`, `invited_last_name` | the admin's identity assertion. `last_name` holds the **full surname incl. any prefix** ("van der Berg") — passport/eIDAS credentials have no separate prefix field, so we mirror that shape |
| `invite_token_hash` | `BYTEA UNIQUE` (hashed like sessions) |
| `expires_at` | `TIMESTAMPTZ NOT NULL` — default **7 days** from issue; `resend` extends it |
| `created_at` | `TIMESTAMPTZ NOT NULL DEFAULT now()` |

`UNIQUE (user_id, organization_id)` (one pending invite per person per org). Invite-time conflicts: an existing active membership → `409 already_member`; an existing pending invitation → `409 already_invited` (the admin should resend, not double-invite). Accept inserts the membership (its PK would reject a duplicate) and deletes the invitation in one tx, so a person can never be both.

**`memberships`** needs no schema change — it already models an active membership.

### Invite-time fields

- **Legal name** (`given_names`, `last_name` — surname incl. any prefix) → asserted on `invitations`, matched against the credential at accept, then the **verified** values populate the user profile. The existing `users.name_prefix` is **removed** (see name-model note); passport/eIDAS credentials don't carry a separate prefix, so a structured prefix can't be verified.
- **`preferred_name`** → **not part of the invite** (an admin shouldn't assert it). It's a **free-form, user-set** field the person sets themselves to anything (e.g. "Bob" for "Robert") — it needn't relate to the legal name. Shown in place of the legal display name where set. Optional; null until the user sets it.
- **`role`, `job_title`, `department_id`** → intended membership attributes on `invitations`, copied to the membership on accept. Org-local, no leak concern.
- A legal-identity credential discloses more than a name (DOB, nationality, document number…). **Default: persist only the name (minimise);** add verified attributes to the profile only if a feature needs them.

### Name model: drop `name_prefix`

The name model collapses to **`given_names` + `last_name`** (surname incl. any prefix), matching what passport/eIDAS credentials provide. The existing `users.name_prefix` is **removed** — keeping a column the authoritative source can never fill is cruft, and splitting a prefix out heuristically would corrupt a *legal* name. Pre-req refactor (small, pre-prod): drop the column from the `users` migration, the `user.User` model, `addMemberRequest`, and the member serialization.

Sorting/display without a stored prefix:
- **Default — sort by the full surname** (so "van der Berg" files under "v"). Zero logic; what most systems and most countries do.
- **If the Dutch "file under the main surname" convention is ever wanted**, derive a sort key by stripping a known-prefix list (a Postgres `GENERATED … STORED` column or an `ORDER BY` expression) — no need to store the prefix. Crucially, a wrong sort key is *cosmetic*, whereas a wrong stored surname is a *legal-data* error — which is exactly why a heuristic is fine for sorting but not for storage. Deferred; recoverable later without the column.

> **EU scope.** `given_names` + `last_name` *is* the eIDAS minimum dataset (first name(s) + family name(s)), so it's the right model for an EU-focused wallet — no worldwide-name hedging needed. The credential is **MRZ-transliterated** (confirmed): names arrive uppercase ASCII, diacritics stripped, prefix folded into the surname — hence the diacritic-fold in the match key (above). Passport names are cleaned to readable casing at write time; the literal MRZ disclosure is kept in the audit log.

---

## Part 3 — Verified consent flow

The Business Wallet discloses **legal identity** (passport / ID / driving licence), so accept is gated on a government-verified name, not just email ownership.

1. **Admin invites** — `POST /orgs/{slug}/members` with email, **full legal name** (given names + surname), role, department. Links/creates the user shell, inserts an `invitations` row (token, `expires_at`, `invited_by`, asserted name, intended role/title/dept). Audits `membership.invited`.
2. **Email** — invitation sent to the address with a link carrying the raw token. (Email sending is the existing TODO; this is its plug-in point.)
3. **Identity disclosure on every accept** — the link lands the recipient in a Yivi **identity disclosure** (name + email from the legal-identity credential), performed *each* time someone joins an org, so every org-join carries its own fresh proof of legal identity.
4. **Accept gates** (all in one `InTx`):
   - **Ownership + token:** the invitation's `user_id` must equal the session user (i.e. `invited_email == disclosed_email`), token valid and not past `expires_at`. A leaked link is useless to anyone else; the token only proves email deliverability and routes the UI.
   - **Disclosed name == invited name** — pure block on mismatch (rules below).
   - **Disclosed name vs. global profile** — reconciliation (below).
5. **On success** — INSERT the `memberships` row from the invitation, refresh the global profile from the verified credential, DELETE the invitation, audit `membership.accepted`.
6. **Decline** — delete the invitation, audit `membership.declined`.

### Name matching rules

Compare **given names + surname** (the credential has no separate prefix field; any prefix is part of the surname). Both the invited-name match and the reconciliation below compare a **derived key**, never the raw strings:
- **case-fold** — Unicode case-folding, *not* naive upper/lower (which mishandle ß, Turkish ı/İ, Greek final sigma);
- **diacritic-fold** to ASCII — a passport is MRZ-transliterated ("Müller"→"MULLER"), so folding makes a passport and an ID-card disclosure of the same name compare equal;
- trim + collapse internal whitespace.

Block on inequality of the *keyed* strings; fix a genuine typo via revoke + re-invite.

**Three separate jobs — don't conflate them.** The MRZ all-caps form is an *encoding artifact*, not the legal name, so we never store it as such:
- **Matching/identity** is anchored by the **derived key** above, computed at compare time. "JOSE" (passport) and "José" (ID card) key-match — no false block or review regardless of which document the person used.
- **The profile stores the best *readable* form** (nice data in the DB): an ID-card disclosure as-is ("van der Berg", "José"); a passport MRZ **cleaned at write time** — title-case + lowercase known name-particles ("van"/"de"/"von"/"del"…) → "van der Berg", "Jose". The particle list is a small bounded heuristic; a wrong guess is purely cosmetic (identity is keyed independently) and it's the same list that can later power prefix-aware sorting. Diacritics aren't recoverable from a passport (accepted) — passport users get correct casing, not diacritics.
- **The faithful, literal disclosure is recorded in the audit log** at verification — that's where "exactly what the credential asserted" lives, immutably, so the mutable profile never has to carry the ugly artifact. The admin's typed invite name is only ever a match input, never stored.

### Identity reconciliation (disclosed vs. global profile)

The disclosed credential is authoritative; the profile is a cache of the last disclosure.

- **First-ever disclosure:** profile has no verified name yet → populate it (cleaned form, per above).
- **Key matches the stored profile** (normal — same person, any document) → proceed; if the new disclosure is a *richer* form (mixed-case/diacritics) than what's stored, upgrade the readable form and never downgrade.
- **Key differs from the stored profile** → the same email previously proved a different verified identity (legal name change or takeover, indistinguishable automatically) → **block for review**:
  - The accept is frozen; an `identity_reviews` record is created and `user.identity_review_required` audited.
  - A **platform admin** (not the org admin — preserves the no-leak rule, trusted cross-org reviewer) compares stored vs. disclosed name and **approves** (update the global identity, let the held accept proceed) or **rejects**. Audit `user.identity_review_resolved`.

`identity_reviews` (new): `id`, `user_id`, `stored_name` snapshot, `disclosed_name` snapshot, `status` (`pending`/`approved`/`rejected`), `reviewed_by`, `reviewed_at`, `created_at`, held-accept context.

---

## PII leak (review findings #1/#2) — invariant

**On every member-returning route:**
- Pending rows come **from `invitations`**: email (the address the admin supplied), `invited_*` name, role, dept, `created_at`, `expires_at`, `invited_by`. There is no profile to join — structurally leak-free.
- Active rows come from `memberships` + the verified `users` profile — the org admin sees the real identity **only post-consent**.
- On a name mismatch or identity review, the discloser's real verified name is **never** surfaced to the org admin (only to the platform reviewer).

Narrows the existence oracle to "is this email a member of *this* org" (which the admin may know); never reveals the person's real name or whether they exist elsewhere.

---

## Deletion: two distinct tiers

- **Org admin removing a member (revoke)** — operational, scoped to the org: delete the `memberships` row (or pending `invitations` row), guard the last **active** admin, audit `membership.revoked`. The user's global profile and audit trail are untouched.
- **Platform-admin GDPR purge** — deployment-wide erasure of a person: **pseudonymise the `users` row in place** (scrub name/email → tombstone, keep the row so `actor_user_id` references stay resolvable and the audit trail stays linkable-but-anonymous) and **hard-delete the operational relationship rows** (pending invitations, active memberships). Audit `user.purged`. Chosen over null-the-actor-reference because it preserves audit linkage (you can still see one pseudonymous actor did N things) and erases identity in a single row. Caveat for legal, not code: erasure has retention exemptions (GDPR Art. 17(3)) — purge erases what's permitted and pseudonymises the rest, never unconditional deletion.

---

## API surface

| route | actor | effect / audit |
|---|---|---|
| `POST /orgs/{slug}/members` | org admin | create invitation, gen token, send email — `membership.invited` |
| `POST /orgs/{slug}/members/{userId}/resend` | org admin | rotate token + bump `expires_at`, resend — `membership.invite_resent` |
| `DELETE /orgs/{slug}/members/{userId}` | org admin | delete invitation or active membership; guard last active admin — `membership.revoked` |
| `PATCH /orgs/{slug}/members/{userId}` | org admin | role/dept change on an **active** member; demoting admin→member keeps the last-active-admin guard (as `UpdateMembership` has today) — `membership.role_changed` |
| `GET /orgs/{slug}/members?status=invited\|active` | org admin | UNION of `memberships` (real profile) + `invitations` (invited-only) |
| `GET /orgs/{slug}/stats` | org admin | active/pending/stale counts, acceptance rate, median time-to-accept |
| `GET /me/invitations` | authed user | my pending invitations across orgs |
| `POST /me/invitations/{token}/accept` | authed user | identity disclosure → gates → membership (or hold for review) — `membership.accepted` |
| `POST /me/invitations/{token}/decline` | authed user | delete invitation — `membership.declined` |
| `GET /admin/identity-reviews`, `POST /admin/identity-reviews/{id}/{approve\|reject}` | platform admin | resolve held mismatches — `user.identity_review_resolved` |

## Touch points

- **`Authorize`, `ListForUser`, `lockAndCountAdmins`** query `memberships` only — correct by construction under model C (no status filter needed).
- **Auth disclosure** — ordinary login discloses **email only** (as today); the **accept flow** uses its own **identity disclosure** (name + email). So `auth.Authenticate` stays email-only and accept gets a separate identity-disclosure session.
- **Expiry** — real-time reject on accept past `expires_at`, plus a sweep (the session pruner is the precedent) that deletes stale `invitations` rows and audits `membership.expired` (actor = system/null).

## Stats (derive, don't store separately)

Current-state counts: active = rows in `memberships`, pending = rows in `invitations`, stale = `invitations` past `expires_at`. Rates and trends from `audit_events` filtered to `membership.*`. The same table backs the FEATURE_LIST:37 transaction log and data-export.

> Business analytics graphs (active employees/month, attestations issued, disclosures verified) belong in a separate metrics/rollup store **fed by** — not querying — the audit log (different retention, access pattern, and PII posture; some data, e.g. disclosures, is daemon-side). Out of scope here; the stable event shapes above keep that aggregation cheap when it lands.

## Rollout notes

- **Schema lands by editing the existing goose migrations** (project convention while pre-release — fold the new `invitations` / `audit_events` / `identity_reviews` tables into existing migration files, no new timestamped migrations), then reset and re-run the `migrate` service to verify.
- **No `memberships` backfill** — existing rows are already active and the schema is unchanged.
- Breaking contract change: `POST .../members` now returns a *pending invitation*, not an active member. Frontend invite UI and member-list rendering branch on pending vs. active.
- Email sending stays stubbed until the email service lands; until then invitations are accept-testable only via a login reached another way (dev).

## Decisions settled

- **GDPR purge:** pseudonymise the `users` row in place; `actor_user_id` is a stable FK; no `actor_email` snapshot.
- **Login disclosure scope:** email-only on ordinary login; identity disclosure on accept.
- **Identity-review hold scope:** freeze the single held accept (not the user's other actions).
- **Invite token TTL:** 7 days, extended by resend.
- **Name:** match/reconcile on a derived key (case-fold + diacritic-fold + whitespace-collapse). Profile stores the best readable form (ID card as-is; passport MRZ cleaned to readable casing at write time — diacritics not recovered); the literal disclosure is kept in the audit log. `preferred_name` is a free-form user-set field (any value), not part of the invite.
- **Audit backfill:** none — pre-prod, start the trail fresh.

## Open / deferred

- **Extra verified attributes** beyond name (DOB, nationality, document number): persist only if a feature needs them — minimise by default.
- **GDPR retention exemptions** (Art. 17(3)) — which records purge may *not* erase: a legal/policy call, not a code one.
