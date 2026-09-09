# Feature: Member re-identification (#240)

**Status:** Implemented (backend + frontend). Members prove a passport/id-card
identity at invitation accept; this adds the lifecycle around that timestamp — a
per-org policy, a derived status, reminder mail on a daily sweep, an admin
on-demand request, and the member-side flow that proves the identity again.
**Slice:** `internal/organization` (policy, status, tokens, scheduler),
`internal/email` (three new kinds), `internal/openid4vpverifier` (the credential
`iat`), plus the members/settings/dashboard surfaces in `frontend/src`.
**Builds on:** `.ai/plans/membership-lifecycle.md` (invite → verified consent →
active) and `.ai/features/auth-openid4vp.md` (`ScopeIdentity`, the disclosure
this flow reuses unchanged).
**Regulation:** `regulation/FEATURE_LIST.md` Art 6(2) — authorisation mappings
must be verifiable, auditable, revocable and traceable, with expired
authorisations automatically detected. A lapsed identification is exactly that.
ISO 9001 7.2 (evidence of competence, dated and attributable) and 8.4 (controls
on externally provided people) are what the employee/external split and the
export of dates are for; ISO/IEC 27001:2022 A.5.18 (periodic review) and A.6.1
(screening repeated on a changed role or contract) map onto the same fields.

---

## 1. What it is

`memberships.identity_verified_at` said *when* someone last proved a legal
identity. On its own that is a fact nobody acts on. This slice turns it into a
lifecycle:

```
 policy (org_identity_settings)          member
   employee: every 12 months               │
   external: every 3 months                │ identity_verified_at
        │                                  ▼
        └──────────▶ identity_due_at ──▶ status: verified · due_soon · overdue
                                   │            │
              daily sweep ─────────┘            └── admin "request identification"
              (reminder / overdue mail)             → status: requested
                     │                                     │
                     └────────── one link ◀────────────────┘
                                    │
                        /reidentify/<token> → ScopeIdentity disclosure
                                    │
                     identity_verified_at = now, due date recomputed
```

Nothing about the disclosure itself is new: re-identification requests the same
`ScopeIdentity` presentation (passport **or** id-card, plus email and phone) the
accept flow uses, runs the same `identity.Reconcile` against the stored name,
and lands in the same audit log. What is new is everything around it.

## 2. Status is derived, never stored

`DeriveIdentityStatus(verifiedAt, dueAt, requestedAt, now, lookaheadDays)`
(`identity_status.go`) is a pure function over the three timestamps:

| status | when |
|---|---|
| `requested` | an admin asked; outranks the rest, because it is the most actionable thing for the member to see |
| `overdue` | `identity_due_at` has passed |
| `due_soon` | `identity_due_at` is within the largest configured reminder threshold |
| `verified` | verified at least once, no deadline pending |
| `never` | never verified (every membership that predates the policy) |

Deriving it means a policy change or the mere passage of time is reflected the
moment a row is read, with no status column to keep in sync and no migration
when the rules change. The handler resolves the org's lookahead once per
request and decorates the rows; it is not per-row work.

`identity_due_at`, by contrast, **is** stored: it is what the reminder sweep
selects on (an index on it), and recomputing it per row per sweep would turn a
cheap query into a scan. It is written in the same transaction as anything that
can change it: accept, an admin-approved identity review, a member-type change,
a completed re-identification, and a settings save (which recomputes every
member of the org at once).

## 3. The policy (`org_identity_settings`)

One row per org, upserted like every other settings slice; `Configured` false
means the feature is **off** — no due dates, no reminders — rather than
defaulting to an interval no admin chose.

| field | meaning |
|---|---|
| `employee_interval_months`, `external_interval_months` | `NULL` = off for that member type. Separate values because externals are usually re-checked more often |
| `reminder_days_before` | e.g. `{30,14,7}`. The largest value doubles as the "due soon" lookahead |
| `overdue_reminder_interval_days`, `overdue_reminder_max_count` | the repeat cadence and its cap once overdue, so nobody is mailed forever |
| `credential_max_age_days` | `NULL` = off. See §5 |
| `overdue_consequence` | `flag` (default) or `block`. See §6 |

Saving audits `identity.settings_updated` with the whole before/after policy, so
the control itself is evidence: an auditor can see when the interval changed and
what it changed to.

## 4. Member type

`member_type` (`employee` | `external`) plus optional free-text
`external_organisation` live on both `invitations` (chosen at invite time,
applied to the membership on accept, exactly as role/job title/department
already flow) and `memberships` (editable by an admin). Changing it recomputes
that member's due date, because the two types can carry different intervals, and
audits `membership.type_changed`.

Entra provisioning could source it (guest users → external, per
`.ai/features/provisioning.md`); nothing here blocks that, and nothing here does
it yet.

## 5. Credential freshness reads the issuer's `iat`

An org can insist the wallet obtained the identity credential recently, so a
re-identification is a fresh act rather than a replay of a credential from years
ago. DCQL 1.0 cannot express it (`values` matches exact equality only, with no range
operator, per OpenID4VP 1.0 §6.3), so the check is server-side after presentation:

- `openid4vpverifier` decodes the **issuer-signed JWT** (the segment before the
  first `~` of the SD-JWT VC) of whichever identity credential was presented and
  reads its `iat` into `Presentation.IdentityIssuedAt`. That claim is the moment
  the wallet obtained the credential from its issuer, not the document's own
  issue date, which is exactly the "re-obtained recently" signal.
- Decoding is not verification. The hosted verifier has already verified
  signature, key binding and trust chain; this only reads an already-trusted
  claim, the same stance `disclosuresOf` takes for the selective disclosures.
- A presentation whose `iat` cannot be read yields the zero time, and the check
  then does not apply: a policy must not reject a valid re-identification
  because a claim was missing.

On rejection the member is told to refresh the credential in their wallet, and
`membership.identity_reverify_rejected` records the reason
(`credential_too_old`) without the disclosed identity.

## 6. Overdue consequence

`flag` (the default) surfaces the status in the list, the detail card, the
member's own banner and the notification catalog, and changes nothing else.

`block` additionally refuses, until the member re-identifies:

- **credential issuance to that member** — `attestation.Handler.issue` asks
  `organization.Store.IsIdentityBlocked` before issuing to a `member` recipient
  and answers `409 recipient_identity_blocked`;
- **signing as a signer** — `signing.OrgMember.IdentityBlocked` (filled by the
  `cmd/api` adapter from `IdentityBlockedSet`) makes `validateSigners` refuse the
  request with `409 signer_identity_blocked`.

Read access and existing sessions are untouched, and suspending the membership
stays out of scope. Both gates are cheap when the policy is off: the store
returns "nobody is blocked" without touching `memberships` unless the org has
actually opted into `block`.

## 7. One link, three entry points

`identity_reverify_tokens` holds one live token per membership, hashed like an
invite token. An admin's request, the reminder mail and the member's own in-app
banner all call `EnsureReverifyToken`, which mints or **rotates** that row, so
an older copy of the link stops working the moment a fresher one is issued, the
same posture as invitation resend. A completed re-identification retires the
token, so it cannot be replayed.

The page (`/reidentify/:token`) is public for the same reason the invite link is:
the member may open the mail on a device with no session, and the token is what
identifies the membership. It is not a bearer key to the account: the flow still
requires a presentation whose disclosed e-mail matches that membership, so a
leaked link is useless without the member's own wallet.

`GET /orgs/{slug}` carries the caller's **own** status (`identity.status`,
`identity.dueAt`) because a plain member cannot read the member list, and the
banner has to work for exactly those members. It exposes nothing about anyone
else.

## 8. Reminders

`IdentityScheduler` sweeps daily (`cmd/api` starts it beside the provisioning
scheduler): for every org with a policy, `IdentityReminderCandidates` selects
the members owed mail and the sweep sends `identity_reminder` (due soon) or
`identity_overdue`, then records the send.

Idempotence is `identity_last_reminder_at` + `identity_reminder_count`: they are
only updated **after** a mail is accepted, so a crash mid-sweep re-sends rather
than silently skipping, and a restart never double-sends within a cadence
window. The count is also the cap.

One deliberate simplification against #240 §6: a member gets **one** advance
reminder when they first enter the lookahead window, plus the repeating overdue
cadence, not one mail per configured threshold. One counter covers the same
member behaviour (act before the deadline, or be reminded until you do) without
tracking which of several thresholds already fired.

## 9. Audit and notifications

New actions, all against `target_type = membership`:
`membership.identity_requested`, `.identity_reverified`,
`.identity_reverify_rejected`, `.identity_reminder_sent`, `.identity_overdue`,
`membership.type_changed`, plus `identity.settings_updated` against
`org_identity_settings`.

The first four (minus the rejection) are in the notification catalog, so an org
can route them to e-mail, Slack or Teams. `.identity_reverify_rejected` is
deliberately **not** subscribable, for the same reason
`membership.accept_rejected` is not: a rejection is about a disclosure the person
made, and that record belongs in the audit log behind access control rather than
in a webhook payload.

## 10. Privacy notice and retention

The lifecycle stores, per membership: `identity_verified_at`, `identity_due_at`,
`identity_requested_at` / `_by`, the reminder cadence counters, `member_type`,
optional `external_organisation`, and (from the earlier slice) `date_of_birth`.

- **Legal basis.** The org's own legitimate interest in knowing that the people
  acting for it are who they say they are, and its ISO 9001 7.2 / 27001 A.5.18
  obligation to be able to show that dated. The identity itself is proved by the
  member's wallet; the org keeps the *outcome and its date*, not the credential.
- **Minimisation.** No document number, no nationality, no place of birth, and
  no copy of the credential. The reason a rejection was refused is stored as a
  code (`email_mismatch`, `name_mismatch`, `credential_too_old`), never as the
  disclosed values.
- **Retention.** Every field above hangs off the membership and is deleted with
  it (`ON DELETE CASCADE`), including the re-identification token. What survives
  an off-boarding is the audit trail, which the GDPR purge pseudonymises in
  place rather than deleting (`.ai/plans/membership-lifecycle.md`).

There is no in-product privacy notice page in this repo yet, so this section is
where that copy has to come from when one is written.

## 11. Not built

- **Member export columns** (#240's export criterion). `internal/export` does
  not exist yet (`.ai/features/export.md` is a design contract for the
  unimplemented #119-#127 series), so there is no exporter to add the columns
  to. When it lands, the fields are on `Member`/`MemberEntry` already.
- **Filtering and sorting the member list by identity status.** The status is
  derived in Go after the SQL page is cut, so a filter on it would have to be
  expressed in SQL to keep the paging totals honest. Left out rather than shipped
  with a count that disagrees with the page.
- **Per-threshold reminder mails** (§8) and **re-identify on every new
  engagement** for externals (#240 §3), which needs an engagement/contract
  concept the wallet does not have.

## 12. Files

| file | holds |
|---|---|
| `internal/migrate/migrations/20260908090000_add_member_type_and_reidentification.sql` | the membership/invitation columns |
| `…090100_create_org_identity_settings.sql`, `…090200_create_identity_reverify_tokens.sql` | the policy and the token |
| `internal/organization/identity_status.go` | the derived status (pure) |
| `internal/organization/identitysettings.go` | policy read/save, due-date recompute, the block gates |
| `internal/organization/member_identity.go` | member type, the admin request (single and bulk) |
| `internal/organization/reverify_store.go`, `reverify_service.go`, `reverify_handler.go` | the token, the disclosure flow, its routes |
| `internal/organization/identity_reminders.go`, `identity_scheduler.go` | candidate selection and the daily sweep |
| `internal/openid4vpverifier/verifier.go` | the issuer `iat` |
| `internal/email/catalog.go` + `templates/defaults.{en,nl}.json` | `identity_reminder`, `identity_overdue`, `identity_requested` |
| `frontend/src/lib/identity-status.ts` | status label/tone, member type label, banner predicate |
| `frontend/src/routes/identity-settings.tsx`, `identity-banner.tsx`, `reidentify.tsx` | the settings panel, the member's banner, the public page |
