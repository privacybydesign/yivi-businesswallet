# Feature: Identity proofing

**Status:** Integrated (backend + frontend). The wallet runs proofing itself, in
its own engine (`internal/proofingengine`, §0); every org is the engine's
tenant under the org's own id, with no provisioning step. The sidebar's "Identity proofing" section holds three pages under
`/{org}/identity-proofing`: **Overview** (the last 30 days' customer sessions
counted, the newest ones, the customers, and an alert per failing webhook),
**Customers** (search, 30-day sessions and verified share, webhook health,
status) and, admin-only, **Flows** (full flow configuration, versioned, and which
flows members may use). A customer's page has tabs: Flows and Sessions for every
member; Branding, API keys, Webhooks and Settings for an admin. "Verify a
person" in its header opens the send form; an admin can pause a customer, after
which no request can be sent for it (`customer_paused`, 409) while sent ones run
out. The customers API is at `/orgs/{slug}/customers`. A member is sent a
request from their member detail page (admin-only, like that page). Customers
are B2B clients with no login: members act for them, and their own backend can
through the public API with one of their keys. Sending creates an engine session
(2, 5 or 10 minutes, per customer) and mails its vcmrtd deep link as a QR code
and a button: the mail is the session, with no wallet page in between. The
outcome lands on the request, in the audit log and at the customer's webhook.
The send form can instead **show the session on this screen**
(`channel: on_screen`, for a subject who is with the member): it opens
`customers/{id}/verify?flow=`, a page in the customer's branding that shows what
the flow collects, lets the subject pick the Yivi app or the Idem app, and only
then creates the request (so the session's minutes start there). Idem shows the
vcmrtd QR and link; Yivi starts an **OpenID4VP** disclosure of the passport or
ID card with its photo at the wallet's own EUDI verifier (`yivi/start`:
`ScopeProofing`, the `openid4vp://` request shown as an `open.yivi.app` universal
link and QR; the transaction id stays in `yivi_transaction_id`), polls
`yivi/disclosure`, which on DONE hands the photo and claims to the engine as the
face reference (`SubmitReference`), then runs the face check from the browser
camera (`yivi/face`): each frame is a Regula image match against that photo
(`MatchImages`), so the Yivi method needs Regula (`ErrMethodUnavailable`
otherwise). A subject
who cancels in the app is not reported: the verifier only knows pending or done,
so the page waits out the countdown. Both count down to the
session's expiry and poll `GET requests/{id}` for the outcome. Nothing is
mailed, and the subject's name and address travel in router state, not the URL.
The hosted page's completion (redirect origins, decline, locale) is in §7.
**Slice:** `internal/proofingengine` (the engine: sessions, flows, the Idem app's
routes, chip verification, results, Regula), `internal/proofingprovider` (the
seam's value types + a test stub, leaf level),
`internal/proofing` (settings, flow selection, members, requests, service,
handler), `internal/email` (kind `identity_proofing_requested`, the `qr` block),
`frontend/src/routes/member-proofing.tsx` (the member detail panel),
`frontend/src/routes/identity-proofing-overview.tsx` (the overview),
`frontend/src/routes/customer-*-tab.tsx` (branding, API keys, webhooks,
settings), `internal/proofing/api_handler.go` (the public API),
`webhook*.go` (endpoint, outbox, deliverer), `internal/safehttp` (the SSRF
guard every externally supplied URL goes through),
`frontend/src/routes/identity-proofing-flows.tsx` (admin: flow editor, versions,
selection), `frontend/src/routes/customers.tsx` + `customer-detail.tsx`
(customers, tabs, assignment, send, requests, pause), `hosted*.go` +
`routes/proof.tsx` (the hosted link, §7), `pause*.go` (§8), `flow_hosted*.go` +
`routes/flow-hosted-settings.tsx` (per-flow hosted settings), `diploma*.go` +
`internal/diploma` + `routes/identity-proofing-flows.tsx` (the flow's diploma
step) + `routes/proofing-verify-steps.tsx` (the upload) (DUO diplomas, §12),
`matchSubject` in `service.go` (one known person, §13).
**Depends on:** the Regula Face API (`REGULA_FACE_API_URL`) for every face
check, and the Idem app's `/api/v1/app/{token}/...` contract
(`vcmrtd/idem/lib/services/proofing_session_client.dart`).

---

## 0. The engine

`internal/proofingengine` runs the proofing sessions: their state machine, the
flow definitions and versions, chip verification, the result with its BSN and
image redaction, assurance scoring and the Regula face checks. Tenants, API
keys, webhooks, the audit log and the admin pages are the wallet's.
`proofing.Service` drives it through Go methods (`rp.go`: flows, create
session, status/result/identity, review, handover, cancel, delete) with a
`proofingprovider.Tenant{ID: org id}`; nothing goes over HTTP. The Idem app
talks to the routes `Engine.Register` mounts under `/api/v1/app/...` (claim,
view, events, steps, submit; left out of the API docs on purpose, since only
the app calls them), and the deep link's `api=` is
`IDENTITY_PROOFING_PUBLIC_URL` (default `APP_BASE_URL`).

- **Storage.** Flow versions in `identity_proofing_flow_versions` (definition as
  JSON, one active version per flow); sessions in `identity_proofing_sessions`,
  the session itself (evidence included) sealed under
  `IDENTITY_PROOFING_ENCRYPTION_KEY`, only token and grant hashes in the clear;
  Regula tags to delete in `identity_proofing_regula_sweeps`. Without the key
  every session is refused (`ErrNoEncryptionKey`); flows still work.
- **Changes reach the wallet in process.** The engine calls
  `Service.SessionChanged` (opened, step started, outcome, expiry). A lazy
  expiry is noticed by whichever read finds the deadline past.
- **Faces.** Only Regula: the native face step (liveness transaction + match
  against DG2) and the Yivi method's per-frame match. A frame already scored
  is counted as a replay without calling Regula again.
  - A face step that failed liveness or did not match may be submitted again:
    how often is the app's call; the engine bounds it at 3 attempts a session
    (`maxFaceStepAttempts`), after which the last failed evidence stands and
    the submit decides on it. Each attempt's transaction is released as usual.
  - A Yivi-method session sends at most twice `BoundLoginMaxAttempts` frames
    to Regula, faceless ones included (`boundLoginFrameBudget`); spending it
    rejects the session `FACE_NO_MATCH`.
    - Each frame reserves its call under the row lock before Regula is
      asked, so concurrent frames cannot overshoot the budget; a frame past
      it decides the session without a call.
  - The member route `yivi/face` counts each frame against its org's
    `MemberFaceFrameLimit` (1500 a minute); past it 429 `rate_limited`. A JPEG2000 chip
  portrait is converted to PNG in pure Go (`images`, go-jpeg2000), refused
  past 4 megapixels (`images.MaxJPEG2000Pixels`) before its raster is decoded.
- **Regula clean-up.** Every Regula tag the app is handed is queued for
  deletion (`identity_proofing_regula_sweeps`): 15 minutes after the session's
  expiry while it runs, moved forward to 15 minutes after its end once it ends,
  whatever the outcome (`Queue.Settle`). An erased session (`DeleteSession`)
  is due at once. `Engine.SweepRegula` deletes them.
- **Retention.** A finished session is deleted 90 days after it ended, unless
  it carries its own retention: a customer's session carries its flow's
  retention override when set, else the customer's data retention, plus a day
  (`engineRetention`), so `PurgeDue` erases it first. `Engine.Purge` (removal
  in batches of 100, each with its own timeout) and `Engine.SweepRegula` run on
  `SESSION_PRUNE_EVERY`.
- **Device trail.** Which device claimed a session, handovers, and refused app
  requests go to the org's audit log (`Config.DeviceTrail` →
  `RequestStore.RecordDeviceEvent`: `identity_proofing.device_claimed`,
  `.device_handed_over`, `.handover_issued`, `.handover_claim_failed`,
  `.access_denied`, target the request; device ids, roles and reasons only).
  Reconnects and device states stay in the server log.
- **Chip access key.** The MRZ-derived key that opens the chip is handed to a
  device only while the chip read is the current step, and dropped once the
  chip evidence lands, so a device taking the session over later never gets
  it.
- **Yivi state is in the session**, sealed (`session.YiviState`: the disclosed
  photo and the run of frames), not in memory, so any API replica scores the
  next frame; it is cleared as the session settles.
- **Change notices** go through one bounded queue (`Engine.Run`), one per
  session while it waits; a full queue drops the notice and the deadline job
  reconciles instead.
- **Claims are rate-limited per session** (`ClaimLimit`, 20 a minute), not per
  client IP: behind the reverse proxy every phone has the proxy's address.
- `IDENTITY_PROOFING_PROVIDER=stub` swaps in `proofingprovider.Stub`, which no
  phone can reach (tests, and `IDENTITY_PROOFING_STUB_OUTCOME` in dev).

---

## 1. Model

- **The org is the engine's tenant: one tenant id, the org's.** A session and a
  flow carry `organizations.id`; there is nothing to provision, no key to hold
  and no enable step.
- **Customers are the wallet's, not engine tenants.** A customer
  (`identity_proofing_customers`) is a wallet row under the org, and every
  session runs on the org's tenant in the engine. A customer
  has no login: members act for it in the UI (the design plan's "org is the only
  party in the UI"), and its own backend uses the customer API with its keys (§4). Flows stay the org's; a customer gets an
  allow-list over them with one default (`identity_proofing_customer_flows`,
  audited `customer_flows_configured` with before/after), independent of the
  members' list: any completable org flow may be assigned. Customers are created,
  renamed and paused or resumed (`customer_created`; `customer_updated` with the
  `name` or `status` before and after; `paused_at` on the row), and removed
  (`customer_removed`), which first purges each of its requests as an erasure
  does (engine session deleted, subject scrubbed from their audit events), then
  deletes them (§4).
- **Flows live in the engine** (`proofingengine/flow`), versioned per org; the
  wallet's "Proofing flows" tab edits them through `proofing.Service`, and it is
  `RequireOrgAdmin`. The rules are `flow.Validate`'s, mirrored in the editor:
  - Steps: `document_capture` (vcmrtd scans the MRZ to unlock the chip) and
    `nfc_read` (NFC chip read) toggle as a pair; `document_photo` (a photo of
    the printed page, `POST /app/{token}/steps/document_photo`) stands
    alone; `face_verification` is one step. `draftSteps` lists
    `document_photo` between `document_capture` and `nfc_read`: the Idem app
    photographs the side it reads the MRZ from as it reads it, cut to the
    frame, and shows it for review (use or retake) right after the scan,
    whatever the listed order: a passport's photo page is then the whole
    photo; a card's MRZ side is its back, so the front is taken next. The engine
    does not enforce the order.
  - Checks: `nfc.passive_auth` is locked on with `nfc_read`, and `face.match`
    with `face_verification`; `nfc.chip_auth` and `face.liveness` are optional;
    a check without its step is unavailable; the threshold is only for
    `face.match`.
  - Requested data: `dg1` comes from the document scan; `dg11`, `dg2` and
    `chip_checks` from NFC; `document_image` from `document_photo`; `selfie`
    and `biometrics` from face. The editor shows plain labels, never these
    codes. Each item is available only with its step and cleared otherwise;
    none is forced on. A flow that requests no data releases the outcome only
    (status and assurance), as the subject is told: the session then carries
    `outcome_only`.
  - Countries: `acceptedIssuingCountries` takes 3-letter ICAO 9303 codes only,
    and a code no country has is refused, so the flow cannot be saved
    (`flow.ValidIssuingCountry`: ISO 3166-1 alpha-3, plus ICAO's own EUE, UNO,
    UNA, UNK, XOM, XPO, XCC, XES, XMP, RKS and the GBD/GBN/GBO/GBP/GBS
    variants). A German document writes "D", which counts as DEU
    (`flow.IssuingStateCode`). The editor checks the 3-letter form first.
  - **Whose identity:** the engine reads the document (name, number, dates,
    nationality, DG11's full name, BSN and place of birth) off the chip
    evidence itself, `documentFromEvidence` in `proofingengine/chipdocument.go`:
    a passport's or ID card's DG1 MRZ and DG11 through gmrtd. The `document`
    the app posts on `/steps/nfc` is ignored: nothing binds it to the chip, so
    a modified app could pair a genuine chip with another person's name. A DG1
    that does not parse is a 400. A driving licence never gets this far: see
    §11.
  - **Assurance level**: none, low or substantial; high is not offered (no
    certified anti-spoofing). `flow.LevelRequirements` (mirrored in the
    editor's `LEVEL_REQUIREMENTS`) says what each needs:
    - low: the chip read, `nfc.passive_auth` verified.
    - substantial: low plus `nfc.chip_auth`, `face.match` and `face.liveness`,
      the face matched by Regula against the chip's DG2.
  - **The level is a minimum, nothing more.** Picking one turns no step, check
    or provider on. A flow whose settings cannot reach it is not saved
    (`reachesAssuranceLevel` in the editor, `flow.Validate` in the engine).
    `draftFromFlow` loads a version as stored.
  - **The level a session achieves** is the highest whose checks all verified
    on the evidence it produced (`computeEIDASAssuranceLevel`):
    - computed without the required level, so it can come out higher;
    - only Regula lifts a face past low;
    - a check that did not apply or run (no AA key, AA not attempted, no
      liveness result) has not verified: low at most.
  - **`requiredChecks` decide no outcome.** They are what the steps perform
    and the score counts. The Idem app follows them, never its own settings:
    Active Authentication only when `nfc.chip_auth` is listed; Regula liveness
    always; the face engine the flow names.
  - **What rejects a session** (`sessionOutcome` in `steps.go`), first match
    wins:

    | Cause | `errorCode` | Audit `reason` |
    |---|---|---|
    | Tampered or cloned chip | `DOC_TAMPERED`, `CHIP_CLONE_DETECTED` | `tamper_detected` |
    | Outside the flow: document type, country, expiry, face step without server evidence (`flowComplianceFailure`) | `DOC_EXPIRED`, … | `flow_policy_violation` |
    | Face step did not verify the person (`faceStepFailure`) | `FACE_NO_MATCH`, `LIVENESS_FAILED` | `check_failed` |
    | Achieved level below the flow's (`flow.MeetsLevel`, fails closed) | `ASSURANCE_NOT_MET` | `assurance_not_met` |

    - **Clone** (`authenticityFailure`):
      - a failed Active Authentication, on any flow;
      - with `nfc.chip_auth` listed and an AA key on the chip, a missing
        response too: a recorded chip read replayed without the chip;
      - without `nfc.chip_auth` the app runs no AA, so a missing response
        only keeps the session from substantial;
      - leaving out a DG15 the EF.SOD lists is `DOC_TAMPERED`
        (`DocumentComplete`, passports and ID cards).
    - **Face step:**
      - `FACE_NO_MATCH` gates at any level;
      - `LIVENESS_FAILED` gates only when the flow requires a level or lists
        `face.liveness`; otherwise the match alone decides;
      - a face that failed liveness never counts as matched, whatever its
        score, and never reaches substantial.
    - `CHIP_AUTH_MISSING` appears only on sessions decided under an earlier
      rule.
  - **The wallet re-checks the level** against the one the request stored at
    send (`required_assurance_level`, `enforceAssurance`): an approval below it
    becomes `rejected` with `ASSURANCE_NOT_MET`. This catches Yivi-method
    sessions, which the engine approves without comparing; one it reports no
    level for counts as low (`yiviEIDASLevel`).
  - Overrides: BSN policy, blur face and blur BSN (inherit/true/false), and
    retention in seconds, which replaces the customer's retention for the
    flow's sessions.
  - Legal basis, purpose and assurance tiers are not in the editor: they are
    carried over unchanged when a version is saved.
  Face capture is forced to `native` (§11). The face provider is always
  `regula` (anything else is 400): the engine has no face engine of its own.
  The subject picks the Yivi app or the Idem app on the verify page
  (`yiviAppAvailable`, the same name in the backend and the frontend), unless the flow
  photographs the document or matches against a reference photo, which only
  the Idem app does. A face step without `nfc_read` needs a
  per-session reference photo (§14): such a flow is not `completable` for
  members, but a customer may be assigned it and send it through its API. A flow with
  `document_photo` (front and back, or a passport's photo page) runs in the
  Idem app only: it rules out the Yivi app. "Edit" is
  `POST /flows/{id}/versions`: the new version is active at once, and a session
  pins the version active when it is created, so sent requests keep theirs
  (`flow_version`). Activating an earlier version rolls back. Audited:
  `flow_created`, `flow_version_created`, `flow_version_activated`, each with
  the full configuration (a flow holds no personal data).
- **Admin allow-list.** `identity_proofing_flow_settings.member_allowed` marks the
  flows an admin made available to members, and `member_default` exactly one
  default (partial unique index). That table is the wallet's one row per org and
  flow id, shared with the hosted and diploma settings below; saving one of the
  three leaves the others alone, and no row reads as every default.
  `PUT /flow-selection` replaces it whole, audited `identity_proofing.flows_configured`
  with before/after. A new flow starts unselected. A request is refused
  (`flow_not_allowed`) on any flow outside the list, for admins too. An id the
  engine does not list is ignored on read. With the stub provider, flows live in memory, so
  a backend restart empties the list while the settings rows stay (and are ignored).
  Stub sessions never decide unless `IDENTITY_PROOFING_STUB_OUTCOME` is set
  (`approved`, `rejected` or `needs_review`); then the stub reports the change
  (`SessionChanged`) 2 s after creation, as the engine would.
- **Language.** A request carries the sender's wallet language (`language`,
  `en`/`nl`, sent by the frontend from its i18n state; optional on the public
  API). It is the request mail's locale and the engine session's `language`,
  which the Idem app gets, so the app shows the same language. Absent, the mail
  uses the deployment default and the app the phone's language.
- **Recipients: a member, or a customer's subject.** A request names either a
  `userId` of the org (`subject_user_id`; any role, employees and externals
  alike) on a flow members may use, or a `customerId` plus an e-mail address and
  an optional name (a CHECK keeps the two exclusive) on a flow assigned to that
  customer (`flow_not_assigned` otherwise). A subject is never a user or member.
  Name and address are a snapshot; `subject_name` is `''` when none was given.
- **Roles:** any member lists allowed flows, customers and a customer's
  assigned flows, sends requests, and sees the requests they sent; creating and
  editing flows, versions and the selection, and creating, renaming and assigning
  flows to customers, are `RequireOrgAdmin`; an admin lists every flow (with
  `allowed`/`default`, or `assigned`/`default` per customer) and every request of
  the org. `GET requests?customerId=` narrows either view to one customer.

## 2. The mail is the session

A session runs the customer's session lifetime (2, 5 or 10 minutes) within the
engine's hard cap `SessionMaxLifetime` (15), and its claim, the `handover`
token in the `vcmrtd://verify?handover=…&api=…` deep link, is single use and
claimable for `ClaimTokenTTL` (10 minutes, never past the session's own
expiry), so the mailed QR works for the whole session.

**Send.** `CreateRequest` creates the engine session (`ttlSeconds` = the
customer's `session_ttl_seconds`, else `proofing.SessionTTL` (600); client
reference = the request id), stores the request
with `link_expires_at` = the session's expiry, attaches the session
(`AttachSession`, audited `identity_proofing.session_created`; `flow_version`
becomes the version the engine pinned), and mails the create response's native claim:
the `qr` block and the button carry the **same** vcmrtd deep link, and the text
states `validMinutes`. A live Idem session with no native claim is an error and
nothing is stored (a Yivi session has none: it starts from the screen). Scan
the QR from inside vcmrtd; the button is for a mail read on the phone.
- **An engine session no request holds is erased** (`discardSession`), since it
  may carry the reference photo:
  - a send whose request fails to store or to attach it, or that got no vcmrtd link;
  - a hosted start that lost the race to attach (409 `link_started`).
  - It runs on its own context (`WithoutCancel`); a failed erase is logged and
    joined to the error returned.

The mail cannot re-mint the claim, and nothing restarts a session: when the
engine reports it `expired` or `cancelled` undecided, `EndSession` stamps
`ips_session_ended_at` (audited `identity_proofing.session_ended`) and the
request reads as expired. A new request means a new mail.

**Mail URLs.** A mail URL is otherwise absolute http(s) only. The proofing
link variable (`proofingUrl`) is declared with `AppScheme: "vcmrtd"`
(`internal/email/catalog.go`): its value must be a `vcmrtd:` link with a host,
checked at render, and only that variable may carry the scheme.

The `api` host in the deep link is `IDENTITY_PROOFING_PUBLIC_URL`, which must
be reachable from the phone. A mail that fails to send leaves the request standing
but answers `mailSent: false`, and the UI says so.

Mail goes through the org's own SMTP settings (`internal/email`); in the dev
stack the seeded org points at Mailpit (`localhost:8025`), so nothing leaves the
machine until an admin sets a real server under e-mail settings.

## 3. Outcomes: notified by the engine, never polled

**Deployment:** the jobs here and the webhook deliverer wait on Postgres
`LISTEN`/`NOTIFY` (`database.RunOnNotify`, one connection held per job). That
needs a direct or session-mode connection: behind a transaction-mode pooler
(PgBouncer's default) a `LISTEN` is lost, and changes are then picked up only
at the next timed wake.

The engine calls `Service.SessionChanged` off its request path on each change
(opened, in_progress, verified, rejected, needs_review, cancelled, expired);
it reconciles that one request (`tryReconcile`, `SessionStatus`, §11). A session nobody finishes is reconciled at its cap by the deadline
job (`ReconcileDue`), which sleeps until the earliest cap and is woken by
`pg_notify` on `identity_proofing_sessions` when a session is attached; one the
engine still reports open past its cap is asked again after 30 s. The job leases
what it re-checks (`ips_reconcile_leased_until`, `FOR UPDATE SKIP LOCKED`), so
API replicas never re-check the same session at once. Nothing runs on a fixed
interval. List reads never call the engine; a single-request read (the on-screen page,
the customer API's `GET sessions/{id}`) re-checks a live request at most once
per `readReconcileEvery` (10 s), as a fallback for a missed notice. A change for a
session not attached yet is dropped; its deadline or next change reconciles it. `needs_review` is not final: a reviewer decides it, or the engine ends it (expired/cancelled), which ends the request as expired and sends
`session.expired`. The UI treats it as not live: it stops polling and shows
"waiting for review". The engine's `opened`/`in_progress` moves the request from `pending` to
`in_progress` (audited `session_started`); an attached session is reconciled
until it is seen to end or decide, also past its cap, so a last-moment outcome
is never lost. `expired` is
derived (no live session and no outcome), never stored. An engine failure during
a read is logged and the last known status is shown.

**Counts.** `GET /identity-proofing/stats` counts the customer requests of the
last `StatsWindow` (30 days) per customer and flow, by outcome, scoped like the
request list (an admin's the org's, a member's their own). Expired is derived
in SQL exactly as `EffectiveStatus` does; cancelled requests have their own
count. The counts are as last reconciled: they read the rows, never the engine, so an outcome no list read has picked up yet is
not in them. The stats query key sits under the requests key, so whatever
refreshes the request lists refreshes the counts.

**No background polling.** `database.RunOnNotify` runs a job on a Postgres
NOTIFY or at the deadline the job returns, never on a ticker; the deadline job
and the customer-webhook deliverer both use it.

**Method and timeline.** `proofingprovider` derives the app a subject used
from the session's status, reading only each device's `role` and whether a
Yivi `disclosure` exists: `yivi_app` (a Yivi disclosure), `idem_app` (the
native device; vcmrtd is the Idem app), `browser` (the web device alone), or
none while no device claimed the session. It is stored on the request
(`method`) by `MarkStarted`, `RecordOutcome` and `EndSession`, and carried in
their audit snapshots. `GET /identity-proofing/requests/{id}/events` is the
request's timeline: its audit events (target `identity_proofing_request`),
oldest first, for an admin or the member who sent it. Everything `reconcile`
records runs under `audit.WithoutActor`: the outcome is the subject's doing,
not that of whoever's read triggered the check. What the subject's app caused, `session_started` and
an outcome not decided in review, names that app instead (`subjectAppContext`,
actor label `app:<method>`, shown as "Idem app"/"Yivi app"); an expiry and a
post-review outcome stay the system's.

## 4. Customer API, webhooks, branding

- **API keys** (`identity_proofing_api_keys`): `yp_live_` + 32 random bytes,
  stored as SHA-256 only, shown once; `prefix` tells keys apart. Revoke is
  permanent; removing the customer removes them. Audited `api_key_created` /
  `api_key_revoked` (with its scopes) on the customer. There are no test keys
  (§5). A customer takes no request, from the dashboard or otherwise, until it
  holds an unrevoked key (409 `customer_no_api_key`; `hasApiKey` on the
  customer, "Setup needed" in the UI).
- **Public API** (`/api/v1/proofing/{flows,sessions,sessions/{id}}`, Bearer
  key, no cookie): acts exactly as a member sending for the customer, on its
  assigned flows (no `flowId` is its default), refused while paused. `sendMail:
  false` skips the mail; the create answer always carries the `deepLink`. A
  request made this way has `requested_by` NULL and `api_key_id` set, and every
  mail for a customer's subject names the customer as requester, never the key.
  `POST /proofing/sessions/{id}/cancel` ends a `pending`/`in_progress` session in
  the engine too (`cancelled_at`; status reads `cancelled`; audited `session_cancelled`).
  `DELETE /proofing/sessions/{id}` erases it in the engine and clears its personal data
  (subject name and address, proofed name, the subject in its audit events;
  `purged_at`, audited `session_purged`, webhook `session.purged`); the row
  stays readable with `purgedAt` and its outcome.
  `GET /proofing/sessions/{id}/result` (scope `results:read`) reads a settled
  session's identity and evidence from the engine on each call (`SessionIdentity`
  decodes only name, birth date, nationality, the checks and the `photo` and
  `selfie` images; never the document number), audited `result_read`; 404 once
  erased. The customer API never returns an image.
  An org admin reads the same shape in the wallet at
  `GET /orgs/{slug}/identity-proofing/requests/{id}/result`
  (`AdminRequestResult`, sharing `requestResult`), audited `result_read` with the
  admin as actor; a member's request is 404. For an approval this response adds
  `photo` (the document's portrait, DG2 or the disclosed credential's) and
  `selfie`, `{mimeType, data}`, only PNG/JPEG/WebP (anything else, like an
  unconverted JPEG2000, is dropped). The customer's Sessions tab reads it when
  an admin opens an approved, rejected or in-review row and folds
  it into the row's detail list with the two photos: one audited read per open
  (`staleTime: Infinity`), and the timeline then shows it.
  Keys carry `scopes` (`sessions:write`, `sessions:read`, `results:read`,
  `flows:read`); every key gets all four, and only a signed-in org admin
  creates one. A missing scope is 403
  `insufficient_scope`.
  `GET /proofing/sessions?limit=&cursor=` pages a customer's sessions newest
  first from stored state (cursor: created_at + id), `{sessions, nextCursor}`.
  `Idempotency-Key` on create, cancel and the headless start (`idempotency.go`,
  `identity_proofing_idempotency_keys`, pruned after 24 h):
  - the same key and body replay the first successful answer
    (`Idempotent-Replayed: true`); another body is 422 `idempotency_key_reused`;
    a call still running is 409 `idempotency_in_flight`.
  - a call that errs or panics releases the key, so it can be retried.
  - a key is never run again once the call may have had its effect: an answer
    that fails to store, or a replica lost mid-call, keeps the key in flight
    (409) until the 24 h prune. There is no timeout release.
  - the stored answer is sealed and linked to its session (`request_id`, read
    from the answer's `id`); `RequestStore.Purge` deletes it.
  Headless: `POST /proofing/sessions/{id}/methods/{idem_app|yivi_app}` starts a
  `hosted: true` session in that app from the customer's own UI (`appLink` or
  `walletLink`; not hosted is 409 `not_hosted`); `GET …/methods/{m}/status`
  answers from stored state. The hosted token is the poll token: the Yivi face
  check and a new Idem code run through `/proof/{token}/…`.
  Sessions are named by an opaque `ps_` id (base32 of the request UUID,
  `public_id.go`) on the customer API and in webhooks; the org routes take
  either form.
  What a key causes is audited with the key as actor: `audit_events.actor_label`
  `api_key:<prefix>`, no `actor_user_id`; the audit log shows "API key <prefix>…".
  Rate-limited per customer (`internal/ratelimit`, in-process token buckets,
  so per API replica): `APICallLimit` 120 calls a minute, and of those
  `APISessionLimit` 10 session creations, so one customer cannot use up the
  deployment's capacity. Past it: 429 `rate_limited` with `Retry-After`.
- **Webhooks**: a customer's results show in Sessions, the API and the audit
  log whatever its webhook. Every event is really sent: without an endpoint of
  its own it goes to the
  wallet's default endpoint (`endpoint_url` NULL, the tab's "Wallet default"),
  `POST /api/v1/identity-proofing/default-webhook` at
  `IDENTITY_PROOFING_DEFAULT_WEBHOOK_URL` (default `APP_BASE_URL` + that path;
  dev: the backend's own `localhost:8080`), signed with a secret HKDF-derived from
  `IDENTITY_PROOFING_ENCRYPTION_KEY` (`Cipher.DeriveSecret`), which the receiver
  checks and acknowledges (204). Sent through a client that may reach a private
  address (the deployment configured it); left out of the customer's endpoint
  health; `test` needs an endpoint of its own. A customer that hosts its own receiver adds one
  endpoint (`identity_proofing_webhooks`, secret
  sealed with the proofing key, `whsec_…`, shown once). Events
  `session.created` / `.started` / `.handover` / `.verified` / `.failed` /
  `.review_opened` / `.expired` / `.cancelled` / `.purged` are written to the
  outbox (`identity_proofing_webhook_deliveries`) in the same transaction as the
  change (`AttachSession`, `MarkStarted`, `RecordHandover`, `RecordOutcome`,
  `EndSession`, `LapseLinks`, `Cancel`, `Purge`); an
  endpoint saved before an event existed is not subscribed to it. `test` is sent on
  request whatever is subscribed. The payload is the session id, status, flow,
  method, assurance and error code: never a name or address (the API has those).
  A delivery's `sessionId` is the `ps_` id (absent for a `test` delivery). Each outbox insert
  `pg_notify`s `identity_proofing_webhooks`, so the deliverer sends as the change
  commits; it then sleeps until the next retry or lapsed lease. It leases due rows (`FOR UPDATE SKIP LOCKED`, 5-minute lease).
  - Per batch (`deliveryBatch` 20): `deliveryWorkers` (10) sends at once, at
    most `endpointWorkers` (2) to one URL (`sendFair`), so an endpoint that
    never answers holds two workers, not other orgs' deliveries.
  - Worst batch: 10 sends deep on one endpoint plus 2 rounds of all workers,
    12 x `safehttp.RequestTimeout` = 120 s, inside the lease
    (`TestDeliveryBatchFitsLease`).

  It POSTs
  through `safehttp` (https, public addresses only, dialed IP = vetted IP, no
  redirects) with `Yivi-Signature: t=<unix>,v1=<hex HMAC-SHA256("<t>.<body>")>`,
  `Yivi-Event`, `Yivi-Delivery`; 8 attempts over about 24 h, then `failed`.
  Health (`delivering` / `failing` since / pending retries) is derived from the
  deliveries; a failing endpoint makes an active customer read "Needs attention".
- **Branding** (columns on the customer): display name, `#rrggbb` colour, logo
  (PNG/JPEG/GIF/WebP, 512 KiB; no SVG, mail clients do not render it), support
  contact, https privacy URL. It applies to the proofing mail of the customer's
  subjects: subject, heading and requester are the display name, the colour
  replaces the org's primary seed, the logo replaces the org's (none shows the
  name as wordmark, never the org's logo), and the `supportContact` /
  `privacyUrl` paragraphs appear only when set (an all-empty paragraph collapses).
- **Settings**: `session_ttl_seconds` (120/300/600) and `data_retention_days`
  (7/30/90/180/365, default 30: the design's maximum of a year; the proofed-name retention). The frontend options are held to the Go
  lists by `lib/identity-proofing.test.ts`.
- **Remove customer** deletes its requests (addresses, names, outcomes), keys,
  endpoint and deliveries; the audit trail stays (`customer_removed`).
  - Each request is purged first (§10), then `CustomerStore.Remove` locks the
    customer row (a request's insert waits on it) and refuses while any request
    is unpurged: one sent meanwhile.
  - The service purges and tries again, `removeCustomerAttempts` (3) times, then
    answers 409 `customer_sessions_left`.

## 5. No test mode

There are no test keys, scripted outcomes or `livemode`: every key and session
is real. `IDENTITY_PROOFING_PROVIDER=stub` is the only stand-in, for dev and
tests (§0).

## 6. A new Idem code mid-session

`POST …/requests/{id}/claim-link` and `POST /proof/{token}/claim-link` call the
engine's `SessionHandover`. An unclaimed slot gets a new claim (the first
lapsed after 10 minutes); a slot whose app went inactive or silent gets a
handover, and the phone that scans takes the session over. Only a handover (the
engine answers `slotClaimed`) is audited `identity_proofing.session_handover` (the
member, or `hosted_link`) and sends `session.handover`; a fresh claim is not. An
app still active is 409 `device_active`. A Yivi request has none
(`wrong_method`).

The on-screen page follows the phone by itself: `GET …/requests/{id}/app` reads
the engine's status live (`devices[].current`/`away`) as `waiting`, `connected` or
`away`, polled every 2 s. It shows the claim QR while `waiting` (renewed once
`deepLinkExpiresAt` or the last code lapses), hides it while `connected`, and
mints a handover QR the moment the app is `away`; the app coming back drops that
code (the engine cancels the grant). If `app` cannot be read it falls back to the
manual "Show a new code", which the hosted page still uses.

## 7. Hosted link

`POST /proofing/sessions` with `hosted: true` (customer API)
creates no engine session: it stores the request with a link token (only its
SHA-256, `link_token_hash`) valid `HostedLinkTTL` (72 h) and answers
`hostedUrl` = `APP_BASE_URL/p/<token>`, which the customer hands its subject.
Nothing is mailed. The public page `/p/:token` (`routes/proof.tsx`) runs the
same steps as the on-screen page (`routes/proofing-verify-steps.tsx`, shared):
what is collected, the app, then its session. Its API is `/api/v1/proof/{token}`
(`hosted_handler.go`), unauthenticated like `/vog/{token}` and rate-limited as
the API is:

- The link is looked up, then the call counts against its customer's
  `HostedCallLimit` across all its links (a member's link counts against its
  org). Past it: 429 `rate_limited` with `Retry-After`.
- Its own bucket, not the API's, sized for the page (face frames every 400 ms).
- As with an unknown API key, a token that matches no link is a 404 and counts
  against nothing.

Its routes: the page, `status`, `start` (once: a second start is 409
`link_started`), the customer's `logo`, and the Yivi `start`/`disclosure`/`face`
routes, which share their implementation with the org routes. The session is
created at `start` for the app the subject picked (`AttachSession` records it)
and runs the customer's session TTL. An unstarted link reads as pending until
it lapses, then expired, in `EffectiveStatus` and the stats alike.

A member makes the same link from the wallet: "Copy a link" in the send form
(`channel: hosted` on `POST .../requests`, which answers `hostedUrl`), for a
person not present and a flow the mail cannot carry (diplomas); the form
shows the link to copy (`SecretReveal`). No redirect: the page ends on its
own done screen.

A link that lapses unstarted is ended by the deadline job (`LapseLinks`,
audited `session_ended` with `reason: link_lapsed`, webhook `session.expired`).

**Consent and completion.** The overview step is the consent screen (who asks,
what is collected, retention, privacy link). The retention it states is the
server's `retentionDays` (hosted view `flow`, customer flow list):
`subjectRetentionDays`, the flow's override else the customer's days, plus the
engine's day of grace, rounded up ("deleted at most N days after"). Its Decline calls
`POST /proof/{token}/decline`, which cancels a link not yet started
(`DeclineHosted` → `RequestStore.Cancel`, audited `session_cancelled` with the
actor label `hosted_link`); a started link is 409 `link_started`. A customer
lists `allowedRedirectOrigins` (customer settings tab, `PATCH` customer; stored
`identity_proofing_customers.allowed_redirect_origins`): https origins, or http
on a loopback host, at most 10, normalised lowercase. A hosted create may carry
`redirectUrl` on one of them (else 400 `redirect_not_allowed`; any other channel
400). The page view answers `sessionId` (the `ps_` id), `redirectUrl`,
`embedOrigins` and `language`. Once `Session` reports an outcome
(`onSettled`), `proof.tsx` posts `{type: "proofing.completed", session, status}`
to its parent for each allowed origin (never `*`), then redirects with
`?session=&status=` when there is a redirect; otherwise it shows its own
outcome (`cancelled` included). A data request in review is not handed back:
the page says it is in review. The page's language is the request's
`language` (also passed to the engine at start), then the browser's, then English
(`lib/hosted-completion.ts`). It switches i18n without persisting the wallet's
stored choice. Framing: the proofing handler is a `server.PageHeaderer`, so the
SPA handler asks it for headers on each index fallback; on `/p/<token>` it sets
`Content-Security-Policy: frame-ancestors <allowed origins>`, or `'none'` for no
origins, an unknown or throttled link, or a failed read (counted against the
link's customer's `HostedCallLimit`). Only where the API serves the SPA (`STATIC_DIR`); in
dev Vite serves it without the header. The rest of the SPA still sends no CSP.
Not built: the full-theme endpoint, and an automated
accessibility test (axe, WCAG 2.2 AA): it needs a rendered DOM, and the frontend
tests run without one by design.
The deep-link gap of §11 applies: the page shows the QR, and says so.

**Per-flow hosted settings** (the `hosted_*` columns of
`identity_proofing_flow_settings`, `flow_hosted.go`; flows belong to the
provider, so keyed on org and flow id with no foreign key): `enabled` (off: a
hosted create is 409 `hosted_disabled`), `locales` (empty: every one; a create
with another `language` is 400; the page keeps to them), and `completion`
(`redirect`, or `done`, where a create with a `redirectUrl` is 400).
Admins edit them under "Hosted page" on a flow in the flows page, with a
preview of the overview step in a customer's branding. No row is the defaults.
**Theme:** the page applies the customer's `primaryColor` through
`applyOrgTheme` (`hostedTheme`) while it shows, and a "Powered by Yivi" line the
customer can leave off (`hide_powered_by`, branding tab).

## 8. Pausing an org's proofing

Two switches, either of which stops it (`identity_proofing_org_pauses`,
`pause.go`): a platform admin's pause (`PUT /admin/organizations/{id}/identity-proofing/pause`,
the "Identity proofing" column on `/admin/organizations`, list at
`GET /admin/identity-proofing/pauses`), which the org cannot lift, and the org
admin's own switch (`PUT /orgs/{slug}/identity-proofing/pause`, the card at the
bottom of the overview). Default: never paused. Audited `identity_proofing.paused`
/ `.resumed` with `by: platform|organization`. While paused, every org route
(the `member`/`admin` closures wrap `h.active`), every customer-API call
(`requireAPIKey`) and every hosted link (`hostedRequest`, so the page, its start
and its `frame-ancestors`) answers 403 `proofing_paused`; the overview shows why
instead. Only the pause routes stay reachable. Running sessions still settle
(engine notices, the deadline job) and webhooks still go out. Pausing (either
level) rejects every open review of the org with `ORG_PAUSED` (reviewer
"system: organisation paused"), so nothing waits on a decision nobody can
make; both pause dialogs say so. No review stays open under a pause:

| When | What |
|---|---|
| Before the pause is set | Every open review is rejected; one that fails refuses the pause. |
| Right after it is set | Open reviews are listed and rejected again (one reached in between). |
| A session reaches review while paused | `tryReconcile` rejects it at once (`rejectIfPaused`). The pause is read after the review is written, and the pause lists reviews after it is set, so one of the two sees the other. |
| A rejection there failed | The hourly `PurgeDue` rejects the reviews still open in every paused org (`rejectPausedReviews`). |

The platform pause asks for confirmation, and both levels record who paused (`platformPausedBy`, `orgPausedBy`). A customer
cannot be paused while one of its sessions waits for review (409
`customer_has_open_reviews`), checked in the pause's own transaction under the
customer row's lock. A review reached after a customer pause stays decidable:
a customer pause refuses only new requests. `Stores.Pauses` nil
(unit tests) never pauses.

## 9. Manual review

A session in review (`needs_review`) waits for a decision (or for the engine to
end it, §3). The reviewer sees the person's identity and check results (an
audited result read). An admin decides it on the customer's Sessions tab (filter
"Needs review", also counted on the overview): approve or reject (optionally
with an error code), with a required reason. `POST .../requests/{id}/review` calls the engine's
`DecideReview` (the admin's e-mail as `reviewer`), audits
`identity_proofing.review_decided` with the admin as actor, then reconciles:
the outcome lands like any other, so an approval still has to meet the flow's
assurance level and the customer's webhook is sent. A request no longer under
review is 409 `not_under_review`. Two reviewers deciding at once are
serialised (`LockReview`). When an identity session goes to review is still a
product decision; today only data requests (§15) do.

## 10. Data minimisation

Stored and audited: status, the engine's assurance tier, achieved eIDAS level,
error code (the reason, shown in words by `proofingRejectionReason`). Each
outcome is its own action, `identity_proofing.approved` / `.rejected` /
`.needs_review`, so a rejection never reads as a success. Every request event
names its subject
(`subjectName` when sent with one, `subjectEmail`). Never the other document fields, BSN or images: the wallet reads only the
status, error code, completion time, assurance and the document's name
(`displayName`, else `firstName lastName`).

The name is the one exception, and only for a **customer's subject** once the
session is **approved** (the sender may know only an address): it is sealed
under `IDENTITY_PROOFING_ENCRYPTION_KEY` in `proofed_name_ciphertext`, shown as
`proofedName`, never audited. A member's request, or a rejected or
`needs_review` one, keeps no name.

A customer's session is purged by the `identity_proofing_purge` pruner
(`Service.PurgeDue`) its flow's retention override (kept on the request,
`retention_override_seconds`) or, without one, its customer's
`data_retention_days` (§4) after it ends
(completed, cancelled, expired; not while it awaits review): as `DELETE`
above, erased in the engine, then its personal data cleared here, the
reviewer's reason in its audit events included. The expected birth date
(§13) goes as soon as the session ends, is cancelled or its link lapses. The
dashboard shows the time as `purgeAt`. A member's request is not purged.
- **`purge_at` is stored** (migration `20261005113000`), so the hourly
  `ListPurgeDue` reads a partial index (`purged_at IS NULL`) instead of
  computing the time for every unpurged row.
  - The store computes it, not a trigger: `setPurgeAt` in
    `request_store.go` is the one rule (retention after settling, a data
    request's review lapse after `dataRequestReviewDays`, NULL for a member's).
  - Every write to one of its inputs calls `refreshPurgeAt` on the same
    transaction, after the write:

    | Write | Input it changes |
    |---|---|
    | `Create` | the row (link lapse, flow retention) |
    | `AttachSession`, `MarkStarted` | session cap, status |
    | `EndSession`, `LapseLinks`, `Purge` | `ips_session_ended_at` |
    | `Cancel` | `cancelled_at` |
    | `RecordOutcome` | status, `completed_at` |

    A new write to one of those columns must call it too;
    `TestPurgeAtSetOnEveryWrite` checks each against the rule.
  - A customer's changed `data_retention_days` (`SaveSettings`) recomputes its
    unpurged requests (`refreshCustomerPurgeAt`). Lock order (rule at
    `lockCustomer`):

    | Transaction | Customer row | Request rows |
    |---|---|---|
    | `SaveSettings` | `FOR NO KEY UPDATE`, first | then all unpurged, `FOR NO KEY UPDATE` in id order |
    | `RequestStore.Create` | `FOR SHARE`, before the insert | its new row |
    | Request writers (`Cancel`, `RecordOutcome`, `Purge`, ...) | `FOR KEY SHARE` (webhook delivery's foreign key), after | their row, first |
    | `SetStatus`, `Remove` | `FOR UPDATE` | read only (`Remove` deletes purged ones) |
    | `ClearExpiredProofedNames` | none | due rows in id order |

    - `FOR SHARE` and `FOR NO KEY UPDATE` conflict, so no request is created
      against the old retention and left stale (`TestRetentionWaitsForInsert`).
    - `FOR KEY SHARE` passes `FOR NO KEY UPDATE`, so a request writer holding
      a row the retention change waits on still enqueues its webhook and
      commits. With `FOR UPDATE` that was a deadlock (40P01,
      `TestRetentionBesideWrite`).
    - Statements that wait on several request rows lock them in id order;
      `LapseLinks` and `ListDue` use `SKIP LOCKED` and never wait.
  - Migration `20261005113000` backfilled existing rows with the same rule, as
    a one-off `UPDATE`.
  - A running session's cap is stored before it passes; the purge asks
    `purge_at <= now()`, which holds only after it anyway. `purgeAt` in the API
    stays empty while the session runs.
- **A purge of a stale read is refused** (`errPurgeSessionMoved`): a request
  read before a hosted start attached its session. `Service.purge` reads it
  again and erases that session first.

## 11. Known gaps to track

- **No browser face step.** The engine has a web slot, but the wallet has no
  browser proofing page for it, so only flows whose face capture runs in the
  app (`selfieLocation: native`) can be sent; the wallet forces `native` and
  refuses others (`flow_not_completable`).
- **Reconciling reads `SessionStatus`**, which carries no personal data; the full
  result is read only for an approved customer subject, whose name is kept.
- **No face engine of its own.** A flow's face step and the Yivi method need
  Regula.
- **EU driving licences** are refused, 422 `document_unsupported`
  (`proofingengine/steps.go`, `flow.DrivingLicence`):
  - at `document_capture`: a `document.type` or `chipAccess.documentType` of
    `drivers_license`;
  - at `nfc_read`: an `mrtdEvidence.documentType` of `eu_driving_licence`,
    before the chip is verified.
  - A flow may not accept one: `flow.Validate` refuses either spelling,
    `drivers_license` or `eu_driving_licence` (`flow.DrivingLicence`), in
    `acceptedDocumentTypes`, so the
    editor's save is a 422 `rejected_by_provider`. The seeded flows accept any
    document type and do not name it.
  - Why: there is no CSCA source for them (`mrtdverify.DrivingLicenceCertPool`
    is empty), so a genuine licence would end `DOC_TAMPERED`.
  - The licence parsing (`drivingLicenceDocument`, `mrtdverify.VerifyPassive`)
    stays, for when a source exists.
- Open product questions are in `.ai/plans/identity-proofing.md`.

## 12. Diplomas (DUO extracts)

For onboarding where a qualification matters (training for professionals: a
new student proves who they are, then which diplomas they hold). Wallet-side
only: the engine knows nothing of it.

- **Per flow** (`identity_proofing_flow_settings.diplomas`, `FlowDiplomaStore`,
  `PUT .../flows/{id}/diplomas`, admin): `off` (the default) or `required`,
  audited `flow_diplomas_configured`. A step is in the flow or not, so there is
  no optional diploma step.
  The flow editor shows it as the last step, "Upload diplomas (DUO)": a step
  for the admin, never sent to the engine (its steps are the app's, and it
  would refuse one it does not know). It is saved after the flow, so a new flow
  gets it once it has an id; the flow list's summary appends it to the
  engine's steps. The flow list carries it as
  `diplomaMode`; a single version's answer (create, edit, versions) omits it.
  A request snapshots it at send (`identity_proofing_requests.diplomas`), as
  it does the assurance level.
- **A page, never a mail.** The extracts are uploaded on the page that ran the
  session, so a diploma flow is refused on the mail channel and as a bare
  deep link (`diplomas_need_page`, 422): on screen or hosted only. The send
  form switches to on-screen by itself (`sendableByMail`).
- **Upload** (`POST .../requests/{id}/diplomas`, `POST /proof/{token}/diplomas`,
  multipart `file` parts): only on an approved request, within
  `DiplomaUploadWindow` (1 h) of its approval (`diplomasUntil` in the
  request, hosted progress), at most 10 per request, 5 MiB each. The page
  shows the step after an approval and hands back (`onSettled`, the hosted
  redirect) only once the subject is done: at least one extract held, or the
  window closed.
- **The check** (`internal/diploma`, as in `privacybydesign/go-diploma-issuer`,
  which has the detail): DUO's PAdES signature is verified first, on the raw
  bytes: it must cover the whole file, carry a qualified timestamp (the
  timestamp's own signer must be trusted) and chain on the EU Trusted Lists to
  DUO's qualified e-seal. An unsigned file is `not_a_diploma`, and only a file
  that passed reaches PDFium (the VOG parser's pool, `vog.PDFiumParser.Pool`),
  which is killed past 20 s. The printed holder (one line, `MatchFullName`)
  and birth date must match the identity the engine approved, read with
  `SessionIdentity` at upload; each name word matches plain or ICAO 9303
  transliterated, so "Müller" matches MUELLER and MULLER.
  `DIPLOMA_VALIDATOR_PROVIDER=duo` is the default; `stub` (set by the dev
  stack, which holds no DUO-signed extract of a test person) accepts every
  signature but still parses and matches for real. `DIPLOMA_TRUST_SOURCE` is
  `eutl` (default: lists cached in `DIPLOMA_TRUST_CACHE_DIR`, reloaded once
  24 h old under their own timeout, a stale cache used when a fetch fails,
  pinned roots as fallback) or `pinned`. `DIPLOMA_OCSP=true` adds an online
  revocation check; off by default.
- **Kept**: what DUO printed about the qualification (type, name, profiles,
  institution, place and date, NLQF/EQF, the number duo.nl/diplomacontrole
  checks, signing time) in `identity_proofing_request_diplomas`. Never the
  PDF, the holder's name or birth date. Audited `diploma_added` (webhook
  `session.diploma_added`) and `diploma_rejected` (reason only:
  `not_a_diploma`, `signature_invalid`, `holder_mismatch`, `duplicate`).
  Shown in the request responses, the customer API's `result`, and the
  Sessions tab. `Purge` deletes them with the rest.
- **Not built**: mailing a hosted link for a diploma flow (the mail is the
  Idem session, §2); diplomas on a member's request page; no real DUO
  extract is in the test suite (personal data), so the parser is covered by
  synthetic layouts only, as upstream verified it against real ones.


## 13. One known person (expected subject)

For a check that must be one particular person, not whoever takes part: an
account recovery (the org's "wachtwoord vergeten" sends its user to a hosted
session) or "Dibran Mulder must identify himself". Wallet-side only: the engine
proofs whoever scans, and the wallet holds the outcome to the person.

- **Send.** A customer's request with a `birthDate` (YYYY-MM-DD, past, with
  `name`): the send form's "Date of birth" (shown only for a flow that reads
  the document data), `POST .../requests`, and the customer API's
  `POST /proofing/sessions`, on every channel. A member's request cannot
  (`ErrInvalidInput`: the wallet holds no birth date of a member), nor a flow
  whose result lacks the document data (`readsIdentity`, the same name in the
  backend and the frontend: `dg1` requested). Without a birth date the name stays a
  label.
- **Kept.** `expects_subject` on the request, and the birth date sealed under
  `IDENTITY_PROOFING_ENCRYPTION_KEY` in `expected_birth_date_ciphertext`
  until the request is decided (`RecordOutcome` approved/rejected), its
  session ends, is cancelled or its link lapses, or it is purged;
  `needs_review` keeps it for the decision. Audited only as
  `expectsSubject: true` on `requested`, never the date.
- **The match** (`matchSubject`, in `tryReconcile` after `enforceAssurance`):
  on an approval it reads `SessionIdentity` and requires the same full name,
  word for word (`diploma.SameName`, plain or ICAO-transliterated; no missing
  or extra name), and the same birth date. A mismatch is recorded as
  `rejected` with `IDENTITY_MISMATCH` (no proofed name kept); a failed
  identity read leaves the request undecided for the next reconcile. A
  review approval is matched too. The engine still holds its approval, so
  the result read (`requestResult` → `heldToVerdict`, customer API and admin
  view alike) answers the wallet's rejection and code with no identity or
  images: the other person is never shown (`ASSURANCE_NOT_MET` likewise).
- **Shown.** `expectedSubject` on the request, the customer API's session
  and its webhooks; "Expected person" in the Sessions tab's details; the
  reason in words (`proofingRejectionReason`).
- **Not built:** the hosted and on-screen pages do not name the expected
  person to the subject.

## 14. The customer's own photo (reference photo)

For a customer that already holds a photo of the person and wants a live face
check against it without the document (an insurer confirming a policy
holder's change of bank account).

- **Flow.** A face step without `nfc_read` (`NeedsReferencePhoto`; the
  editor's "Face check" alone). `Completable` stays false for it (a member
  has no photo to send); `CustomerCompletable` is true, so it can be
  assigned to a customer, and the flow lists carry `needsReferencePhoto`.
  It runs in the Idem app only (`yiviAppAvailable` false: the Yivi app
  matches against its credential's photo) and reaches no eIDAS level (the
  levels need the chip; the engine scores it with `chipReference` false).
  The send form leaves it out.
- **Send.** Only the customer API: `referencePhoto`, standard base64,
  sniffed as PNG/JPEG/WebP, at most `MaxReferencePhotoBytes` (512 KiB;
  the create body is capped at 1 MiB, an idempotent one past that is 413
  `body_too_large`). Required on such a flow (422
  `reference_photo_required`), refused on any other (400), so a chip flow is
  never matched against a supplied photo. Audited as `referencePhoto:
  true`, never the image.
- **Held.** The engine's session keeps it sealed (`session.ReferencePhoto`)
  and hands it to the app as `faceReference` for the face step
  (`faceMatchReference`); `Session.ForStorage` drops it once the session is
  under review or over (the result's copy, below, is all that stays). A hosted request
  holds it sealed (`reference_photo_ciphertext`) only until its subject
  starts (`AttachSession`), cancels, lets the link lapse, or it is purged;
  `RequestStore.ReferencePhoto` reads it at start only, never with a list.
- **In the result.** The session's result marks `faceReference:
  relying_party` (the evidence reads `reference_photo`, not `emrtd`) and,
  when the flow requests the selfie, releases the photo beside it
  (`referencePhoto`), kept and purged with the selfie: the Sessions tab
  shows "Customer's photo" next to the selfie. The customer API's result
  carries no image, as ever.
- **Not built:** sending it from the wallet's own send form; combining it
  with the expected-subject match (§13), which needs the document data.

## 15. Data requests (GDPR access and erasure)

A person asks a customer what is held of them ("see my data", Art. 15) or for
its erasure ("delete my data", Art. 17). The customer sends them a session on
a flow of that kind, like any other session (API, hosted link, mail or
on-screen); the person proves who they are, and the session goes to the
customer's **needs review**, where an org admin decides. Code:
`data_request*.go`; frontend `routes/data-request-review.tsx`, the flow
editor's Type, and the hosted page.

- **Flow kind.** `identity_proofing_flow_settings.kind`: `identity` (the
  default), `data_access` or `data_erasure`, set in the flow editor
  (`PUT …/flows/{id}/kind`, audited `identity_proofing.flow_kind_configured`).
  A data request flow must read the name and date of birth
  (`flow_no_identity`) and is never a members' flow (`data_flow_for_member`,
  a CHECK and both service paths); it asks for no diplomas. A request keeps
  the kind it was sent with (`identity_proofing_requests.flow_kind`).
- **To review, not approved.** When the engine approves a data request's
  person, `tryReconcile` calls `findDataMatches` and records `needs_review`
  (sending `session.review_opened`) instead of approving; it keeps the proofed
  name like an approval. From then on the wallet owns the decision: a later
  engine read (approved, or the session purged at the engine) changes nothing.
- **Matches.** `findDataMatches` reads the person's identity and every
  `Candidates` session of the **same customer** (not purged, settled; the
  person's earlier data requests too once decided, as each holds who asked, so
  an erasure clears them; one still in review never), skipping one whose kept
  proofed name lacks the family name. A match needs the same full name word for
  word (`diploma.SameName`) and birth date: `strong` when document type,
  issuing state and expiry also agree (the same document; the document number
  is never read), `probable` otherwise. The customer's unfinished sessions
  (pending, in progress, expired, cancelled) sent to exactly the same e-mail
  address are listed apart as `email` matches: they hold no proofed identity.
  Stored in `identity_proofing_request_matches`. No HMAC or other deterministic
  identifier: it would link a person's sessions for anyone reading the
  database.
- **Review.** The session's panel shows the person's identity and proof (name,
  date of birth, document, chip, face match, liveness, eIDAS level; the
  result read allows a data request in review, `heldToVerdict`) and the
  matches. Only `strong` matches start ticked; `probable` and `email` ones are
  taken only when ticked, and the API's approve without `requestIds` takes the
  `strong` ones alone. `DecideReview` → `decideDataRequest` (decided in the
  engine too if it holds a review there). Approving an erasure asks to confirm
  the number of sessions, then purges each approved match through
  `Service.purge` (row stays, personal data goes, `session.purged` per
  session), then the request itself. Access sets `data_export_until`
  (`DataExportWindow`, 7 days; 24 h through the hosted link). Reject records
  `MANUAL_REVIEW_REJECTED` unless a code is given.
- **Nobody reviews it.** A data request left in review 30 days (GDPR Art.
  12(3)) is rejected `REVIEW_LAPSED` and purged by `PurgeDue`.
- **Export.** An approved access request's data: per approved session the
  outcome, the contact the customer gave, identity and evidence, diplomas;
  images named in `imagesHeld`, never included. For the person on their
  hosted link (`GET /proof/{token}/data-export`, `dataExportUntil` in the
  progress; 24 h), for the admin (`…/requests/{id}/data-export`) and for the
  customer (`GET /proofing/sessions/{id}/data-export`). Audited
  `identity_proofing.data_exported`.
- **Not built:** matching across customers or orgs (each customer is the
  controller of its own sessions); a mail to the person when decided (a hosted
  person reopens the link; otherwise the customer tells them); members' own
  proofing requests.
