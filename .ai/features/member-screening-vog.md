# Feature: Member screening / VOG (#242)

**Status:** Implemented (backend + frontend), **not yet enabled for real tenants** -
see §8. Members prove they hold a valid certificate of conduct (VOG) either by
uploading the PDF, validated live against Justis' validatie.nl, or, once an org
opts in, by disclosing the `pbdf.vog` credential. The org keeps only the
outcome and the dates; the PDF itself is never persisted.
**Slice:** `internal/vog` (the Justis-specific validator/parser, no dependency
on `organization`), `internal/organization` (screening store/service/handler,
scheduler), `internal/openid4vpverifier` + `internal/auth` (the credential
disclosure), `internal/email` (three new kinds), plus the member/settings
surfaces in `frontend/src`.
**Builds on:** `.ai/features/member-reidentification.md` (#240) - this reuses
its member type, `date_of_birth`, and mirrors its policy/status/scheduler
shape throughout, deliberately.
**Regulation:** the same Art 6(2) (auditable, traceable, revocable
authorisation-adjacent state) and GDPR data-minimisation angle #240 cites -
a screening record is exactly that.

---

## 1. What it is

```
 policy (org_screening_settings)         member_screenings (history)
   required_for: employees                 one row per attempt
   required_codes: [11, 43]                result, covered/missing codes,
   recheck: 12 months                       valid_until, checked_by
        │                                          │
        └──────────▶ memberships.vog_* (denormalised latest-attempt state)
                              │
                    status: not_required · none · valid · expiring ·
                            expired · rejected · recheck_required · requested
                              │
              PDF upload ─────┼───── pbdf.vog credential (opt-in)
           (always on,             (opt-in, aspect codes only)
            validatie.nl)
```

A screening decision is the same four checks regardless of method: does the
document validate/disclose at all, does its name and date of birth match the
member's stored identity, does it cover every code the org requires, is it
recent enough. `ScreeningService.evaluateAndFinish` (`screening_service.go`)
is that shared decision after a `vog.Document` is in hand; `UploadVog` builds
the `Document` by validating and parsing a PDF, `DiscloseVogCredential` builds
it from the credential's disclosed claims.

## 2. The record: `member_screenings` + denormalised membership columns

`member_screenings` is append-only history, one row per attempt (valid or not
- a rejection is worth keeping). Per #242's minimisation design it stores only
what a future decision needs: `result`, `checked_at`, `vog_issue_date`,
`valid_until`, `covered_codes`/`missing_codes` (the **org-required subset**
only, never the document's full code list), a keyed hash of the kenmerk
(`reference_hash`, HMAC - `VOG_REFERENCE_HASH_KEY`, optional; no key means no
hash is written, never an unkeyed one), and who ran it. No PDF, no name, no
place of birth, no purpose text.

`memberships` carries three columns mirroring the *latest* attempt only:
`vog_last_result`, `vog_valid_until`, `vog_covered_codes`. The list/detail
screens and the reminder sweep read these, never `member_screenings`
directly, so paging members never joins screening history per row. All three
are written by `RecordScreening` in the same transaction as the history
insert (`internal/organization/screening_store.go`).

**"Latest attempt wins."** A fresh failed re-check overrides an older,
still-unexpired valid record: `vog_valid_until` is set to `NULL` whenever the
latest result is not `valid`, even if a previous attempt had not yet expired.
The alternative (keep showing "valid" until the old one's own expiry) was
considered and rejected as confusing when the most recent evidence
contradicts it - see `DeriveScreeningStatus`'s doc comment in
`screening_status.go`.

## 3. Status is derived, never stored

`DeriveScreeningStatus` (`screening_status.go`) is a pure function over the
denormalised columns plus the org's *current* required codes, mirroring
`DeriveIdentityStatus`'s shape:

| status | when |
|---|---|
| `not_required` | the policy does not require a VOG for this member's type |
| `requested` | an admin asked; outranks everything below |
| `none` | required, no attempt on file |
| `rejected` | the latest attempt did not pass (rejected/mismatch/insufficient_scope) |
| `recheck_required` | the latest attempt passed, but its covered codes do not cover *today's* requirement |
| `expired` / `expiring` / `valid` | `vog_valid_until` against now and the lookahead window |

`recheck_required` is why `covered_codes` matters: a record only ever proves
the subset it was evaluated against at check time, so when an org adds a
required code, an old `valid` record cannot silently keep validating it -
it becomes `recheck_required` and the member is asked to submit again.
Nothing is rewritten to produce this; it falls out of comparing stored
`covered_codes` to the live requirement on every read.

## 4. The policy (`org_screening_settings`)

One row per org, upserted like `org_identity_settings`; no row means
`required_for = "nobody"` - off, not defaulting to a requirement nobody chose.

| field | meaning |
|---|---|
| `required_for` | `nobody` / `employees` / `externals` / `both` |
| `required_codes` | function-aspect and/or specific-profile codes a VOG must cover |
| `max_age_at_upload_days` | `NULL` = off; rejects a VOG whose issue date is older |
| `employee_recheck_interval_months` / `external_recheck_interval_months` | `NULL` = off for that type |
| `recheck_anchor` | `issue_date` (default) or `checked_at` - which date the interval counts from |
| `accept_yivi_credential` | opt-in to the credential path, off by default |
| reminder fields, `overdue_consequence` | shaped exactly like `org_identity_settings`'s |

Saving recomputes `vog_valid_until` for every member whose latest attempt was
valid (`recomputeScreeningValidUntilTx`), using a **scalar subquery correlated
to the `UPDATE` target**, not a `FROM ... LATERAL` join - Postgres does not
allow a `LATERAL` subquery in an `UPDATE`'s `FROM` list to reference the
update's own target table, which is not obvious and cost a failed integration
test run to discover.

`overdue_consequence` accepts `block` but **only `flag` does anything today**
- unlike identity's block gate (refusing issuance/signing), this PR does not
wire a VOG-overdue gate into `attestation`/`signing`. The issue's own
acceptance criteria do not ask for one; adding it is a follow-up.

## 5. The PDF path: validatie.nl + parsing

`internal/vog` is deliberately independent of `organization` - it only knows
about Justis' document, not about members or orgs.

- **`validator.go`** is the external-provider seam
  (`.ai/conventions/BACKEND.md`'s pattern): `HTTPClient` (real,
  `VOG_VALIDATOR_PROVIDER=validatie_nl`) or `StubValidator` (dev/CI default),
  chosen in `cmd/api/main.go` exactly like `registryprovider`/`qerdsprovider`.
  `Validate` retries a transient answer (a retryable GAAV code, or a
  transport/5xx error) up to three times with 1s/2s backoff; a decisive
  answer (authentic or a final rejection) returns immediately. The
  multipart part's `Content-Type` is set explicitly to `application/pdf` -
  go-vog-issuer's confirmed gotcha: Go's default `application/octet-stream`
  makes GAAV answer code 2 (unknown document) for a genuine VOG.
  `Ping` GETs the same URL and accepts a `405` as healthy (the service has no
  health endpoint; a `405` on `GET` proves the route exists and answered).
- **`parser.go`** is a port of go-vog-issuer's parser, the one confirmed
  against real Justis documents: PDFium in a WebAssembly sandbox
  (`klippa-app/go-pdfium` + wazero, pure Go, no cgo) opens the PDF and yields
  every positioned text run; `ExtractDocument` then reads the labelled fields
  from the value column right of the Dutch label (`kenmerk`, `Datum`,
  `Geslachtsnaam`, `Tussenvoegsels`, `Voorna(a)m(en)`, `Geboortedatum`) and
  the two-digit codes on the line(s) after `profiel:`. Dates are written out
  in Dutch (`25 maart 2026`, `ParseDutchDate`). `PDFiumParser` is built once
  in `cmd/api/main.go` (compiling the module takes seconds) and injected into
  `ScreeningService` through the `vogParser` seam; tests use a fake.
  `ErrNotAVOG` (unreadable, or no "Verklaring Omtrent het Gedrag" title) and
  `ErrUnparseable` (a VOG whose required field could not be read) are the two
  recorded verdicts; any other parser error is an infrastructure failure and
  surfaces as a 500 without recording anything against the member.

**Why not `digitorus/pdf`.** The first version reached for it (already a
dependency via PAdES signing) and failed every real VOG as "unparseable":
Justis PDFs are AES-encrypted (V=4 / AESV2, empty user password) with the
crypt-filter `/Length` written in bits (128), and `digitorus/pdf`'s V4 check
accepts only the byte form (16) - in v0.1.2 and still in v0.2.0. Its string
decryption for AES is also a `panic("AES not implemented")` in v0.1.2. The
layout was wrong too: the fields are columns (English label, Dutch label,
value), not `label: value` lines, and the given-names label is
`Voorna(a)m(en)`, not `Voornamen`.

**Verified against real documents.** `internal/vog/vogtest` embeds two genuine
VOGs (issued to a project member, contributed as fixtures; go-vog-issuer's
suite uses the same two) with the values printed on them.
`internal/vog`'s `TestParseRealVOG` and `internal/integration`'s VOG flows run
through the actual encrypted PDFs, so a parser regression against a real
Justis document fails CI. PDFium compiles in `TestMain` once per test binary
(~5s; ~25s under `-race`), and lazily in the integration package so a run
without `TEST_DATABASE_URL` never pays for it.

## 6. Function-aspect codes: intentionally code-only

`vog.FunctionAspects` lists the 19 function-aspect codes go-vog-issuer
confirmed empirically (the published API-specificatie GAAV v1.0 table is
garbled). This PR does **not** ship human-readable Dutch/English descriptions
per code - I have no reliable source for Justis' own text for each one, and
fabricating plausible-looking regulatory copy in a compliance product is worse
than not having it. The settings screen shows "Aspect 11", "Aspect 12", etc.
Sourcing Justis' own table and adding real descriptions is a follow-up.

Specific-profile numbers (two-digit codes outside the 19 function aspects)
are accepted as free-form input in the settings screen (`extraCodes`) since
there is no authoritative closed list to validate against here either -
`vog.IsFunctionAspect` is the only closed classification this code makes.

## 7. Matching

Both paths compare the parsed/disclosed name and date of birth against the
member's **stored** identity (`ScreeningMatchContext`, read from
`users.given_names/last_name` + `memberships.date_of_birth`) using
`identity.Name.Key()` - the same case/diacritic-folding key every other
identity comparison in this backend uses. `ErrVogNoDateOfBirth` is returned,
before any validatie.nl call, when the membership has no date of birth yet
(a legacy member who predates #240's persistence of it). The frontend
(`vog-submit.tsx`) then does not send them away: see §7a.

## 7a. The submission link, and a member who never identified

A member submits on a **public page keyed by a bearer token**
(`/vog/:token`, `vog-submit.tsx`), the same shape as a credential claim
(`/claim/:token`) or re-identification (`/reidentify/:token`): no sign-in.
`vog_submit_tokens` holds one live token per membership, hashed like the
re-identification token (`screening_token_store.go`). An admin's request
(minted inside `RequestVog`'s transaction), the reminder sweep and the
dashboard banner (`POST /orgs/{slug}/me/vog-token`) all mint or **rotate** it,
so an older link stops working. A **valid** screening retires it in
`RecordScreening`'s transaction; a rejected one keeps it so the member can
retry from the same mail. A leaked link cannot plant a VOG: every path still
matches the member's stored name and date of birth against a
Justis-validated PDF or a wallet credential. The frontend reads success from
the mutation, not a refetched preview, because the refetch would 404.

`GET /vog/{token}` answers with the member's state (`needsIdentity`: no date
of birth on file; `acceptCredential`). When `needsIdentity` is set, or a check
just came back `no_date_of_birth`, the page offers two ways in instead of a
dead end:

- **Identify first** (`POST /vog/{token}/identity-session` +
  `/identity-complete`): an identity disclosure for the membership the token
  names. `Service.CompleteOwnIdentification` backs it. On success the page
  switches to the ordinary upload and credential sections.
- **Identity + VOG in one wallet session** (when the org accepts the
  credential): `POST /vog/{token}/identity-credential-session` +
  `/identity-credential-complete`, `openid4vpverifier.ScopeIdentityVog` - the
  identity query plus the `pbdf.vog` credential as a fourth, independently
  required credential set. `ScreeningService.DiscloseIdentityAndVogCredential`
  applies the identity half first and evaluates the VOG half against the date
  of birth it just disclosed - never against the VOG's own say-so. A rejected
  identity half records no screening.

Both, and the token-based re-identification, share one rule set:
`Service.ApplyDisclosedIdentity` (e-mail must match, name reconciled with
`identity.Reconcile` - `Populate` writes the name for a member with none on
file, `Review` rejects - credential freshness enforced, then
`Store.CompleteReverification`). `ScreeningService` reaches it through the
`identityRecorder` seam. Rejections surface with the re-identification codes
the frontend already has copy for (`email_mismatch`, `name_mismatch`,
`credential_too_old`).

**Per-credential claims.** Both the identity credential and `pbdf.vog` carry
a `dateOfBirth`; `openid4vpverifier.Presentation.Claims` flattens across
credentials in map order, so in a combined presentation one would overwrite
the other at random. `Presentation.ByCredential` keeps them apart and
`IdentityClaims()` / `VogClaims()` are what `extractIdentity` / `extractVog`
read; a hand-built `Presentation` without `ByCredential` falls back to the
flat map, so every existing test and fake keeps working.

## 8. The opt-in `pbdf.vog` credential

`ScreeningSettings.AcceptYiviCredential` gates a second method, disclosed via
the same hosted OpenID4VP verifier every other disclosure uses
(`openid4vpverifier.ScopeVog`). The DCQL claim list is **dynamic**: only the
credential's core identity fields (`issueDate`, `surname`, `prefix`,
`givenNames`, `dateOfBirth`) plus the `aspectNN` yes/no claim for each
function-aspect code the org actually requires - never every `aspectNN` flag,
never `profileCodes` as a blob. `StartPresentation` grew a variadic
`claims ...string` parameter for this; every existing caller is unaffected.

**A specific-profile requirement can never be satisfied by the credential
path.** The credential only carries per-aspect yes/no flags, nothing for a
specific-profile number, so `requiredAspectCodes` filters the org's
requirement down to the function-aspect subset before building the DCQL
query, and any specific-profile code in the requirement always ends up in
`missingCodes` for a credential-disclosed attempt. This is the honest
consequence of the minimisation design (not requesting `profileCodes`), not a
bug.

**The `aspectNN` claim's yes/no encoding is unverified.** `auth.isAffirmative`
accepts `"yes"/"true"/"1"/"ja"` case-insensitively; I do not have the actual
issued credential to confirm its literal attribute value. Worth checking
before this path is relied upon.

`auth.Service` gained `StartVogSession`/`DiscloseVog` alongside the existing
`StartIdentitySession`/`DiscloseIdentity`, following the same shape;
`organization.ScreeningService` depends on it through a local `vogDiscloser`
interface (`screening_service.go`), never importing `auth` more than that.

## 9. Reminders and scheduler

`ScreeningScheduler` (`screening_scheduler.go`) sweeps daily, structurally
identical to `IdentityScheduler`: `VogReminderCandidates` selects members
whose `vog_valid_until` has just entered the lookahead window (one advance
reminder) or is past it (repeating, capped, cadence), `RecordVogReminderSent`
bumps the counters and audits. New mail kinds `vog_requested`, `vog_reminder`,
`vog_expired` (`internal/email`).

**Every mail carries a fresh link.** The reminder sweep mints (rotates) the
member's VOG token per mail, exactly as `IdentityScheduler` does for
re-identification, and links to `/vog/<token>` (§7a). An earlier design linked
into the authenticated app instead; it was replaced so a member can submit
from the mail on any device without signing in, like a credential claim.

## 10. Audit and notifications

`membership.vog_requested`, `.vog_checked`, `.vog_rejected`, `.vog_mismatch`,
`.vog_insufficient_scope`, `.vog_reminder_sent`, `.vog_expired`, plus
`screening.settings_updated` against `org_screening_settings`.

`.vog_rejected`/`.vog_mismatch`/`.vog_insufficient_scope` are **not**
subscribable as notifications, for the same reason
`membership.identity_reverify_rejected` is not: each concerns a document the
member submitted, and that belongs in the audit log behind access control,
not a webhook payload. `.vog_requested`, `.vog_checked`, `.vog_reminder_sent`,
`.vog_expired` are in the catalog.

## 11. Privacy, retention and the Justis terms-of-use blocker

**Legal basis and minimisation** mirror #240 §10: the org's legitimate
interest (and, for regulated sectors, legal obligation) in knowing the people
acting for it have been screened, evidenced by outcome and date rather than
by the document. `member_screenings` cascades with the membership
(composite FK to `memberships(user_id, organization_id)`, `ON DELETE
CASCADE`), the same operational-record posture as `identity_reverify_tokens`.

**Justis terms-of-use: not confirmed.** The issue's own acceptance criteria
require this recorded here *before the feature is enabled for tenants* - it
is not confirmed, and I have no channel to Justis from this environment.
`ScreeningSettings.RequiredFor` defaults to `nobody` (off) with no row until
an admin configures one, which keeps the feature inert by default, but that
is not the same thing as legal confirmation. **Do not turn this on for a real
tenant until that conversation has happened.**

**DPIA / privacy notice**: not written, same gap #240 left open (no
in-product privacy-notice page exists in this repo yet); this document is
where that copy should come from once one does.

## 12. Not built

- **Human descriptions per function-aspect/specific-profile code** (§6).
- **Overdue-block enforcement** for VOG (§4) - the setting exists, only
  `flag` does anything.
- **Member export columns** - `internal/export` still does not exist
  (`.ai/features/export.md`), same gap #240 left for VOG's fields too.
- **Verification against a real issued `pbdf.vog` credential** (§8) - no
  wallet holding the credential in this environment. The PDF parser *is*
  verified against real documents (§5).

## 13. Files

| file | holds |
|---|---|
| `internal/migrate/migrations/20260914090000_add_member_screening_columns.sql` | the membership columns |
| `…20260923090000_create_vog_submit_tokens.sql`, `internal/organization/screening_token_store.go`, `screening_token_handler.go` | the submission link (§7a): token table, mint/lookup, public preview, identify-first, the banner's mint route |
| `…090100_create_org_screening_settings.sql`, `…090200_create_member_screenings.sql` | the policy and history table |
| `internal/vog/vog.go`, `validator.go`, `parser.go` | the Justis-specific validator/parser (PDFium via WebAssembly), independent of `organization` |
| `internal/vog/vogtest/samples.go` + `testdata/*.pdf` | two real Justis VOGs with their printed values, for `internal/vog`'s and `internal/integration`'s tests |
| `internal/organization/screening_status.go` | the derived status (pure) |
| `internal/organization/screening_settings_store.go` | policy read/save, recompute |
| `internal/organization/screening_store.go` | `RecordScreening`, history, admin request |
| `internal/organization/screening_service.go` | the shared PDF/credential decision logic |
| `internal/organization/screening_upload_handler.go`, `screening_credential_handler.go`, `members_screening.go` | the HTTP routes: token-keyed upload and credential paths incl. the combined identity+VOG session (§7a), admin upload/request/history |
| `internal/organization/reverify_service.go` (`ApplyDisclosedIdentity`, `CompleteOwnIdentification`) | the shared identity rule set and the identify-first path (§7a) |
| `internal/organization/member_insights.go` | the admin's org-wide count of members by identity/screening status (`.ai/features/member-insights.md`) |
| `internal/organization/screening_reminders.go`, `screening_scheduler.go` | candidate selection and the daily sweep |
| `internal/openid4vpverifier/dcql.go`, `verifier.go`; `internal/auth/service.go`, `disclosure.go` | the `pbdf.vog` DCQL query and disclosure read |
| `internal/email/catalog.go` + `templates/defaults.{en,nl}.json` | `vog_requested`, `vog_reminder`, `vog_expired` |
| `frontend/src/lib/screening-status.ts`, `vog-codes.ts` | status label/tone/hint, the 19 aspect codes |
| `frontend/src/routes/screening-settings.tsx`, `vog-submit.tsx`, `vog-banner.tsx`, `members.tsx` | the settings panel, the public submission page (§7a), the dashboard banner (mints a link), the member list's VOG column (shown only when the policy requires a VOG from anyone, `screeningConfigured`) |
| `frontend/src/api/vog-link.ts`, `vog-link.queries.ts` | the token-keyed API the submission page uses |
| `frontend/src/lib/vog-page.ts` | when the page asks for identity first, which error codes are identity rejections |
