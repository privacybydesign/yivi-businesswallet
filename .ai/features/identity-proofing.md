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
(`vcmrtd/idem/lib/services/proofing_session_client.dart`). It no longer depends
on `privacybydesign/identity-proofing-service` ("IPS"); "IPS" below names the
behaviour the engine kept from it.

---

## 0. The engine

`internal/proofingengine` is IPS's session engine folded into the wallet
(ported at IPS `6ef3e19`), minus what the wallet already owns (tenants, API
keys, webhooks, audit, admin pages) and minus IPS's TFLite face engine.
`proofing.Service` drives it through Go methods (`rp.go`: flows, create
session, status/result/identity, review, handover, cancel, delete) with a
`proofingprovider.Tenant{ID: org id, Sandbox: test mode}`; nothing goes over
HTTP. The Idem app talks to the routes `Engine.Register` mounts under
`/api/v1/app/...` (claim, view, events, steps, submit; left out of the API
docs on purpose, only the app calls them), unchanged from IPS, so
the vcmrtd deep link's `api=` is `IDENTITY_PROOFING_PUBLIC_URL` (default
`APP_BASE_URL`).

- **Storage.** Flow versions in `identity_proofing_flow_versions` (definition as
  JSON, one active version per flow); sessions in `identity_proofing_sessions`,
  the session itself (evidence included) sealed under
  `IDENTITY_PROOFING_ENCRYPTION_KEY`, only token and grant hashes in the clear;
  Regula tags to delete in `identity_proofing_regula_sweeps`. Without the key
  every session is refused (`ErrNoEncryptionKey`); flows still work.
- **Changes reach the wallet in process.** Where IPS pushed a signed webhook,
  the engine calls `Service.SessionChanged` (opened, step started, outcome,
  expiry). A lazy expiry is noticed by whichever read finds the deadline past.
- **Faces.** Only Regula: the native face step (liveness transaction + match
  against DG2) and the Yivi method's per-frame match. The `engine` face
  provider is refused; a JPEG2000 chip portrait is converted to PNG only in a
  `-tags jpeg2000` build (ImageMagick via cgo), so the default build hands
  Regula and the browser the original bytes.
- **Retention.** A finished session is deleted 90 days after it ended
  (IPS's default). A customer's session carries the customer's data retention
  plus a day (`engineRetention`), so `PurgeDue` erases it first.
  `Engine.Purge` and `Engine.SweepRegula` run on `SESSION_PRUNE_EVERY`.
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
  and no enable step. (Under IPS the wallet created a tenant per org with a live
  and a test key in `org_identity_proofing_settings`, audited
  `identity_proofing.provisioned`; that table is dropped and old audit rows
  keep the action.)
- **Customers are the wallet's, not IPS sub-tenants.** IPS sub-tenants only
  override privacy knobs (BSN policy, blurring), share the parent's keys and
  cannot own flows, so a customer (`identity_proofing_customers`) is a wallet row
  under the org and every session still runs on the org's IPS tenant. A customer
  has no login: members act for it in the UI (the design plan's "org is the only
  party in the UI"), and its own backend uses the customer API with its keys (§4). Flows stay the org's; a customer gets an
  allow-list over them with one default (`identity_proofing_customer_flows`,
  audited `customer_flows_configured` with before/after), independent of the
  members' list: any completable org flow may be assigned. Customers are created,
  renamed and paused or resumed (`customer_created`; `customer_updated` with the
  `name` or `status` before and after; `paused_at` on the row), and removed
  (`customer_removed`), which first deletes its requests: a request references
  its customer `ON DELETE RESTRICT`, so nothing is deleted by cascade (§4). This deliberately differs from
  `.ai/plans/identity-proofing.md`, where each customer owns its flows.
- **Flows live at IPS**, versioned, managed like IPS's own admin page
  (`/api/v1/db-test/admin`): that page, not the tenant API docs, is the editor
  the wallet's "Proofing flows" tab mirrors. It calls `/api/v1/flows` with the
  org's tenant key (IPS has no admin-key flow routes), and it is `RequireOrgAdmin`
  in the wallet. The rules copy IPS (`flow.Validate` + the admin page's
  `syncStageDependencies`):
  - Steps: `document_capture` (vcmrtd scans the MRZ to unlock the chip) and
    `nfc_read` (NFC chip read) toggle as a pair; `document_photo` (a photo of
    the printed page, IPS `POST /app/{token}/steps/document_photo`) stands
    alone; `face_verification` is one step. `draftSteps` lists
    `document_photo` between `document_capture` and `nfc_read`: the Idem app
    photographs the side it reads the MRZ from as it reads it, cut to the
    frame, and shows it for review (use or retake) right after the scan,
    whatever the listed order: a passport's photo page is then the whole
    photo; a card's MRZ side is its back, so the front is taken next. IPS does not enforce order.
  - Checks: `nfc.passive_auth` is locked on with `nfc_read`, and `face.match`
    with `face_verification`; `nfc.chip_auth` and `face.liveness` are optional;
    a check without its step is unavailable; the threshold is only for
    `face.match`.
  - Requested data: `dg1` comes from the document scan; `dg11`, `dg2` and
    `chip_checks` from NFC; `document_image` from `document_photo`; `selfie`
    and `biometrics` from face. The editor shows plain labels, never these
    codes. Each item is
    available only with its step and cleared otherwise. None is forced on (IPS
    forces nothing).
  - Assurance level: none/low/substantial; high is not offered (no certified
    anti-spoofing). One table says what each level needs,
    `flow.LevelRequirements`, mirrored in the editor's `LEVEL_REQUIREMENTS`:
    low = chip read with `nfc.passive_auth` verified (genuine evidence held);
    substantial = low plus `nfc.chip_auth`, `face.match` and `face.liveness`,
    the face verified by Regula against the chip's DG2. Picking a level turns
    those steps, checks and the provider on and locks them
    (`withAssuranceLevel`; `draftFromFlow` applies it to an older version
    too), and `flow.Validate` refuses a flow whose checks or provider do not
    meet its level. A session's eIDAS level is the highest whose every check
    verified (`computeEIDASAssuranceLevel`), and is only calculated when the
    flow sets a level: without one it is empty, sandbox included. A check that
    did not apply (a chip without an Active Authentication key) has not
    verified, so such a document reaches low at most. In a session the Idem app follows the
    flow, never its own settings: Active Authentication exactly when the
    flow lists `nfc.chip_auth`, active liveness when it lists
    `face.liveness` (IPS's app view carries `requiredChecks`), and the
    face engine the flow's provider names (`faceProvider`: Regula, or the
    on-device engine for `engine`). IPS only
    *declares* it: it approves on its checks and never compares the eIDAS level
    a session achieved with it. The wallet does: a request stores its flow's
    level at send (`required_assurance_level`), and an approval below it is
    recorded as `rejected` with `ASSURANCE_NOT_MET` (`enforceAssurance`,
    `MeetsAssurance`; an unknown level fails closed). IPS scores a Yivi
    session itself; for an IPS that reports no level the wallet counts an
    approved one as `low` (`yiviEIDASLevel`: face match, no liveness, IPS's
    own rule). Whether Yivi is offered stays the face
    provider's call alone (`YiviAppAvailable`).
  - Overrides: BSN policy, blur face and blur BSN (inherit/true/false),
    retention in seconds.
  - Legal basis, purpose and assurance tiers are not on IPS's editor, so not on
    the wallet's: they are carried over unchanged when a version is saved.
  Face capture is forced to `native` (§11). The face provider (`faceProvider`:
  `regula`, `engine` or `Iris`) is chosen per flow and always named, `regula`
  when none is given: IPS's empty default silently falls back to its engine
  when Regula is not configured there. IPS fails the face step closed on any
  other capture. The provider also decides the subject's app choice on the
  verify page: a flow whose face step is on a provider the Yivi app does not
  have (`idemOnlyFaceProviders`: Iris) skips the method step and starts the
  Idem app; every other flow lets the subject pick Yivi or Idem
  (`YiviAppAvailable`, mirrored by `yiviAppAvailable`). IPS's Yivi face check
  (bound login) always scores on its own engine, whatever the flow names. A face step without `nfc_read` needs a
  per-session reference photo (§14): such a flow is not `completable` for
  members, but a customer may be assigned it and send it through its API. A flow with
  `document_photo` (front and back, or a passport's photo page) runs in the
  Idem app only: it rules out the Yivi app. "Edit" is
  `POST /flows/{id}/versions`: the new version is active at once, and a session
  pins the version active when it is created, so sent requests keep theirs
  (`flow_version`). Activating an earlier version rolls back. Audited:
  `flow_created`, `flow_version_created`, `flow_version_activated`, each with
  the full configuration (a flow holds no personal data).
- **Admin allow-list.** `org_identity_proofing_flows` holds only the flow ids an
  admin made available to members, and exactly one default (partial unique index).
  `PUT /flow-selection` replaces it whole, audited `identity_proofing.flows_configured`
  with before/after. A new flow starts unselected. A request is refused
  (`flow_not_allowed`) on any flow outside the list, for admins too. An id IPS no
  longer lists is ignored on read. With the stub provider, flows live in memory, so
  a backend restart empties the list while the selection rows stay (and are ignored).
  Stub sessions never decide unless `IDENTITY_PROOFING_STUB_OUTCOME` is set
  (`approved`, `rejected` or `needs_review`); then the stub pushes the change
  (`SessionChanged`) 2 s after creation, as IPS would.
- **Language.** A request carries the sender's wallet language (`language`,
  `en`/`nl`, sent by the frontend from its i18n state; optional on the public
  API). It is the request mail's locale and the IPS session's `language`, which
  IPS hands the Idem app, so the app shows the same language. Absent, the mail
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

This copies IPS's own timing (`identity-proofing-service`,
`backend/internal/api/api.go` `DefaultConfig`): a session runs
`SessionCreateTTL` (10 minutes) within the hard cap `SessionMaxLifetime`
(15), and its claim, the `handover` token in the `vcmrtd://verify?handover=…&api=…`
deep link, is claimable for `ClaimTokenTTL`, single use. IPS keeps a claim
claimable as long as a default session (10 minutes, never past the session's
own expiry), so the mailed QR works for the whole session.

**Send.** `CreateRequest` creates the IPS session (`ttlSeconds` = the
customer's `session_ttl_seconds`, else `proofing.SessionTTL` (600); client
reference = the request id), stores the request
with `link_expires_at` = the session's expiry, attaches the session
(`AttachSession`, audited `identity_proofing.session_created`; `flow_version`
becomes the version IPS pinned), and mails the create response's native claim:
the `qr` block and the button carry the **same** vcmrtd deep link, and the text
states `validMinutes`. A live Idem session with no native claim is an error and
nothing is stored (a Yivi session has none: it starts from the screen). A session whose request then fails to store lapses unused at IPS. Scan
the QR from inside vcmrtd; the button is for a mail read on the phone.

The mail cannot re-mint the claim, and nothing restarts a session: when IPS
reports it `expired` or `cancelled` undecided, `EndSession` stamps
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

The engine calls `Service.SessionChanged` off its request path on each change
(opened, in_progress, verified, rejected, needs_review, cancelled, expired);
it reconciles that one request (`tryReconcile`, `SessionStatus`, §11). A session nobody finishes is reconciled at its cap by the deadline
job (`ReconcileDue`), which sleeps until the earliest cap and is woken by
`pg_notify` on `identity_proofing_sessions` when a session is attached; one IPS
still reports open past its cap is asked again after 30 s. The job leases what
it re-checks (`ips_reconcile_leased_until`, `FOR UPDATE SKIP LOCKED`), so API
replicas never ask IPS about the same session at once. Nothing runs on a fixed
interval. List reads never call IPS; a single-request read (the on-screen page,
the customer API's `GET sessions/{id}`) re-checks a live request at most once
per `readReconcileEvery` (10 s), as a fallback for a missed push. A change for a
session not attached yet is dropped; its deadline or next change reconciles it. `needs_review` is not final: IPS decides it,
or ends it (expired/cancelled), which ends the request as expired and sends
`session.expired`. The UI treats it as not live: it stops polling and shows
"waiting for review". IPS `opened`/`in_progress` moves the request from `pending` to
`in_progress` (audited `session_started`); an attached session is reconciled
until it is seen to end or decide, also past its cap, so a last-moment outcome
is never lost. `expired` is
derived (no live session and no outcome), never stored. An IPS failure during a read is
logged and the last known status is shown.

**Counts.** `GET /identity-proofing/stats` counts the customer requests of the
last `StatsWindow` (30 days) per customer and flow, by outcome, scoped like the
request list (an admin's the org's, a member's their own). Expired is derived
in SQL exactly as `EffectiveStatus` does. The counts are as last reconciled:
they read the rows, never IPS, so an outcome no list read has picked up yet is
not in them. The stats query key sits under the requests key, so whatever
refreshes the request lists refreshes the counts.

**No background polling.** `database.RunOnNotify` runs a job on a Postgres
NOTIFY or at the deadline the job returns, never on a ticker; the deadline job
and the customer-webhook deliverer both use it.

**Method and timeline.** `proofingprovider` derives the app a subject used
from the `/status` (or `/result`) answer, reading only each device's `role` and whether a
Yivi `disclosure` exists: `yivi_app` (a Yivi disclosure), `idem_app` (IPS's
native device; vcmrtd is the Idem app), `browser` (the web device alone), or
none while no device claimed the session. It is stored on the request
(`method`) by `MarkStarted`, `RecordOutcome` and `EndSession`, and carried in
their audit snapshots. `GET /identity-proofing/requests/{id}/events` is the
request's timeline: its audit events (target `identity_proofing_request`),
oldest first, for an admin or the member who sent it. Everything `reconcile`
records runs under `audit.WithoutActor`: the outcome is the subject's doing,
not that of whoever's read triggered the check (rows written before this
still name the reader). What the subject's app caused, `session_started` and
an outcome not decided in review, names that app instead (`subjectAppContext`,
actor label `app:<method>`, shown as "Idem app"/"Yivi app"); an expiry and a
post-review outcome stay the system's.

## 4. Customer API, webhooks, branding

- **API keys** (`identity_proofing_api_keys`): `yp_live_` + 32 random bytes,
  stored as SHA-256 only, shown once; `prefix` tells keys apart. Revoke is
  permanent; removing the customer removes them. Audited `api_key_created` /
  `api_key_revoked` (with its scopes) on the customer. Test keys: §5. A customer takes no live
  request, from the dashboard or otherwise, until it holds an unrevoked live key
  (409 `customer_no_api_key`; `hasLiveKey` on the customer, "Setup needed" in
  the UI); test requests need only their test key.
- **Public API** (`/api/v1/proofing/{flows,sessions,sessions/{id}}`, Bearer
  key, no cookie): acts exactly as a member sending for the customer, on its
  assigned flows (no `flowId` is its default), refused while paused. `sendMail:
  false` skips the mail; the create answer always carries the `deepLink`. A
  request made this way has `requested_by` NULL and `api_key_id` set, and every
  mail for a customer's subject names the customer as requester, never the key.
  `POST /proofing/sessions/{id}/cancel` ends a `pending`/`in_progress` session at
  IPS too (`cancelled_at`; status reads `cancelled`; audited `session_cancelled`).
  `DELETE /proofing/sessions/{id}` erases it at IPS and clears its personal data
  (subject name and address, proofed name, the subject in its audit events;
  `purged_at`, audited `session_purged`, webhook `session.purged`); the row
  stays readable with `purgedAt` and its outcome.
  `GET /proofing/sessions/{id}/result` (scope `results:read`) reads a settled
  session's identity and evidence from IPS on each call (`SessionIdentity`
  decodes only name, birth date, nationality, the checks and the `photo` and
  `selfie` images; never the document number), audited `result_read`; 404 once
  erased. The customer API never returns an image.
  An org admin reads the same shape in the wallet at
  `GET /orgs/{slug}/identity-proofing/requests/{id}/result`
  (`AdminRequestResult`, sharing `requestResult`), audited `result_read` with the
  admin as actor; a member's request is 404. For an approval this response adds
  `photo` (the document's portrait, DG2 or the disclosed credential's) and
  `selfie`, `{mimeType, data}`, only PNG/JPEG/WebP (anything else, like an
  unconverted JPEG2000, is dropped in `proofingprovider`). The customer's
  Sessions tab reads it when an admin opens an approved or rejected row and folds
  it into the row's detail list with the two photos: one audited read per open
  (`staleTime: Infinity`), and the timeline then shows it.
  Keys carry `scopes` (`sessions:write`, `sessions:read`, `results:read`,
  `flows:read`); every key gets all four, and only a signed-in org admin
  creates one. A missing scope is 403
  `insufficient_scope`.
  `GET /proofing/sessions?limit=&cursor=` pages a customer's sessions newest
  first from stored state (cursor: created_at + id), `{sessions, nextCursor}`.
  `Idempotency-Key` on create and cancel (`idempotency.go`,
  `identity_proofing_idempotency_keys`, pruned after 24 h): the same key and body
  replay the first successful answer (`Idempotent-Replayed: true`), another
  body is 422 `idempotency_key_reused`, a call still running 409
  `idempotency_in_flight`; a call that errs keeps nothing, so it can be retried.
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
  `APISessionLimit` 10 session creations, since IPS's session limit (§11) is
  shared by every customer. Past it: 429 `rate_limited` with `Retry-After`.
- **Webhooks**: a customer's results show in Sessions, the API and the audit
  log whatever its webhook. Like IPS, every event is really sent: without an
  endpoint of its own it goes to the
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
  `livemode`, method, assurance and error code: never a name or address (the API has those). Each outbox insert
  `pg_notify`s `identity_proofing_webhooks`, so the deliverer sends as the change
  commits; it then sleeps until the next retry or lapsed lease. It leases due rows (`FOR UPDATE SKIP LOCKED`, 5-minute lease), POSTs
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

## 5. Test mode

A customer key is `live` (`yp_live_`) or `test` (`yp_test_`), fixed at creation
(`identity_proofing_api_keys.mode`, audited on `api_key_created`). A test key's
`POST /proofing/sessions` runs on the org's own tenant in test mode
(`Tenant.Sandbox`), the engine's sandbox: it only creates
scripted-outcome sessions, and a live key never can. The session is created
without a flow and resolves at once to `scriptedOutcome` (`approve` by default,
`reject:<CODE>`, `needs_review`, `expire`; IPS's own sandbox): the wallet still
checks the flow is assigned to the customer and records it on the request. A
test request (`mode = test`) is reconciled right away, never mailed, has no
deep link, is left out of the stats, and carries `livemode: false` in the API
answer and its webhooks. A live key sending `scriptedOutcome` is refused
(400). The request's mode picks the tenant it is reconciled under.

## 6. A new Idem code mid-session

`POST …/requests/{id}/claim-link` and `POST /proof/{token}/claim-link` call the
engine's `SessionHandover`. An unclaimed slot gets a new claim (the first
lapsed after 10 minutes); a slot whose app went inactive or silent gets a
handover, and the phone that scans takes the session over. Only a handover (IPS
answers `slotClaimed`) is audited `identity_proofing.session_handover` (the
member, or `hosted_link`) and sends `session.handover`; a fresh claim is not. An
app still active is 409 `device_active`. A Yivi or test request has none
(`wrong_method`).

The on-screen page follows the phone by itself: `GET …/requests/{id}/app` reads
IPS's status live (`devices[].current`/`away`) as `waiting`, `connected` or
`away`, polled every 2 s. It shows the claim QR while `waiting` (renewed once
`deepLinkExpiresAt` or the last code lapses), hides it while `connected`, and
mints a handover QR the moment the app is `away`; the app coming back drops that
code (IPS cancels the grant). If `app` cannot be read it falls back to the
manual "Show a new code", which the hosted page still uses.

## 7. Hosted link

`POST /proofing/sessions` with `hosted: true` (customer API, live keys only)
creates no IPS session: it stores the request with a link token (only its
SHA-256, `link_token_hash`) valid `HostedLinkTTL` (72 h) and answers
`hostedUrl` = `APP_BASE_URL/p/<token>`, which the customer hands its subject.
Nothing is mailed. The public page `/p/:token` (`routes/proof.tsx`) runs the
same steps as the on-screen page (`routes/proofing-verify-steps.tsx`, shared):
what is collected, the app, then its session. Its API is `/api/v1/proof/{token}`
(`hosted_handler.go`), unauthenticated like `/vog/{token}` and limited per link
(`HostedCallLimit`, keyed by the token's hash; the wallet has no trusted
client IP): the page, `status`, `start` (once: a second start is 409
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
what is collected, retention, privacy link). Its Decline calls
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
outcome (`cancelled` included). The page's language is the request's
`language` (also passed to IPS at start), then the browser's, then English
(`lib/hosted-completion.ts`). It switches i18n without persisting the wallet's
stored choice. Framing: the proofing handler is a `server.PageHeaderer`, so the
SPA handler asks it for headers on each index fallback; on `/p/<token>` it sets
`Content-Security-Policy: frame-ancestors <allowed origins>`, or `'none'` for no
origins, an unknown or throttled link, or a failed read (counted against the
link's `HostedCallLimit`). Only where the API serves the SPA (`STATIC_DIR`); in
dev Vite serves it without the header. The rest of the SPA still sends no CSP.
Not built: the full-theme endpoint, and an automated
accessibility test (axe, WCAG 2.2 AA): it needs a rendered DOM, and the frontend
tests run without one by design.
The deep-link gap of §11 applies: the page shows the QR, and says so.

**Per-flow hosted settings** (`identity_proofing_flow_hosted_settings`,
`flow_hosted.go`; flows live at IPS, so keyed on org and flow id): `enabled`
(off: a hosted create is 409 `hosted_disabled`), `locales` (empty: every one; a
create with another `language` is 400; the page keeps to them), and
`completion` (`redirect`, or `done`, where a create with a `redirectUrl` is 400).
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
(IPS events, the deadline job) and webhooks still go out. `Stores.Pauses` nil
(unit tests) never pauses.

## 9. Manual review

A session IPS sends to review (`needs_review`) waits for a decision (or for IPS
to end it, §3). An admin decides it on the customer's Sessions tab (filter
"Needs review", also counted on the overview): approve or reject (optionally
with an error code), with a required reason. `POST .../requests/{id}/review` calls the engine's
`DecideReview` (the admin's e-mail as `reviewer`), audits
`identity_proofing.review_decided` with the admin as actor, then reconciles:
the outcome lands like any other, so an approval still has to meet the flow's
assurance level and the customer's webhook is sent. A request no longer under
review is 409 `not_under_review`. When IPS sends a real session to review is
still a product decision; test mode's `scriptedOutcome: needs_review` exercises
the path meanwhile.

## 10. Data minimisation

Stored and audited: status, IPS assurance tier, achieved eIDAS level, IPS error
code (the reason, shown in words by `proofingRejectionReason`). Each outcome is
its own action, `identity_proofing.approved` / `.rejected` / `.needs_review`, so
a rejection never reads as a success; `identity_proofing.completed` is only on
rows written before that split. Every request event names its subject
(`subjectName` when sent with one, `subjectEmail`). Never the other document fields, BSN or images. `proofingprovider` decodes
only `status/errorCode/completedAt/result.assurance` and the document's name
(`displayName`, else `firstName lastName`).

The name is the one exception, and only for a **customer's subject** once the
session is **approved** (the sender may know only an address): it is sealed
under `IDENTITY_PROOFING_ENCRYPTION_KEY` in `proofed_name_ciphertext`, shown as
`proofedName`, never audited. A member's request, or a rejected or
`needs_review` one, keeps no name.

A customer's session is purged by the `identity_proofing_purge` pruner
(`Service.PurgeDue`) its customer's `data_retention_days` (§4) after it ends
(completed, cancelled, expired; not while it awaits review): as `DELETE`
above, erased in the engine, then its personal data cleared here. The dashboard shows
the time as `purgeAt`. A member's request is not purged.

## 11. Known gaps to track

- **No end-user web page.** The engine keeps IPS's web slot but the wallet has
  no browser proofing page, so only flows whose face capture runs in the app
  (`selfieLocation: native`) can be sent; the wallet forces `native` and refuses
  others (`flow_not_completable`).
- **Deep link tap does not open vcmrtd yet**; scanning the QR from inside the app
  works.
- **Reconciling reads `SessionStatus`**, which carries no personal data; the full
  result is read only for an approved customer subject, whose name is kept.
- **No face engine.** A flow's face step and the Yivi method need Regula; IPS
  flows on its `engine` provider cannot run here.
- **Flows did not move.** Flows created at IPS are not copied into the engine:
  an org recreates them. `20261001180000_remove_identity_proofing_ips_flow_config`
  removes what named an IPS flow (the members' allow-list, customer
  assignments, hosted and diploma settings); requests keep their flow id and
  name as history, and one in flight at IPS during the switch settles as
  expired (the engine does not know its session).

## 12. Diplomas (DUO extracts)

For onboarding where a qualification matters (training for professionals: a
new student proves who they are, then which diplomas they hold). Wallet-side
only: the engine knows nothing of it.

- **Per flow** (`identity_proofing_flow_diploma_settings`, `FlowDiplomaStore`,
  `PUT .../flows/{id}/diplomas`, admin): `off` (no row) or `required`, audited
  `flow_diplomas_configured`. A step is in the flow or not, so there is no
  optional diploma step (an earlier `optional` was migrated to `required`).
  The flow editor shows it as the last step, "Upload diplomas (DUO)": a step
  for the admin, never sent to IPS (its steps are the app's, and it would
  refuse one it does not know). It is saved after the flow, so a new flow
  gets it once it has an id; the flow list's summary appends it to the IPS
  steps. The flow list carries it as
  `diplomaMode`; a single version's answer (create, edit, versions) omits it.
  A request snapshots it at send (`identity_proofing_requests.diplomas`), as
  it does the assurance level; a test request is always `off`.
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
- **The check** (`internal/diploma`, ported from `privacybydesign/go-diploma-issuer`,
  which has the detail): PDFium (the VOG parser's pool, `vog.PDFiumParser.Pool`)
  reads the extract; DUO's PAdES signature must cover the whole file, carry a
  qualified timestamp and chain on the EU Trusted Lists to DUO's qualified
  e-seal; the printed holder (one line, `MatchFullName`) and birth date must
  match the identity IPS approved, read with `SessionIdentity` at upload.
  `DIPLOMA_VALIDATOR_PROVIDER=stub` (default; dev/CI hold no DUO-signed
  extract of a test person) accepts every signature but still parses and
  matches for real; `duo` checks it, with `DIPLOMA_TRUST_SOURCE`
  `eutl` (default, lists cached in `DIPLOMA_TRUST_CACHE_DIR`, reloaded on use
  once 24 h old, pinned roots as fallback) or `pinned`. OCSP is off.
- **Kept**: what DUO printed about the qualification (type, name, profiles,
  institution, place and date, NLQF/EQF, the number duo.nl/diplomacontrole
  checks, signing time) in `identity_proofing_request_diplomas`. Never the
  PDF, the holder's name or birth date. Audited `diploma_added` (webhook
  `session.diploma_added`) and `diploma_rejected` (reason only:
  `not_a_diploma`, `signature_invalid`, `holder_mismatch`, `duplicate`).
  Shown in the request responses, the customer API's `result`, and the
  Sessions tab. `Purge` deletes them with the rest.
- **Not built**: mailing a hosted link for a diploma flow (the mail is the
  vcmrtd session, §2); diplomas on a member's request page; no real DUO
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
  whose result lacks the document data (`ReadsIdentity`, mirrored by
  `readsIdentity`: `dg1` requested, or `document_capture` when the flow
  lists no data). Without a birth date the name stays a label.
- **Kept.** `expects_subject` on the request, and the birth date sealed under
  `IDENTITY_PROOFING_ENCRYPTION_KEY` in `expected_birth_date_ciphertext`
  until the request is decided (`RecordOutcome` approved/rejected) or
  purged; `needs_review` keeps it for the decision. Audited only as
  `expectsSubject: true` on `requested`, never the date.
- **The match** (`matchSubject`, in `tryReconcile` after `enforceAssurance`):
  on an approval it reads `SessionIdentity` and runs
  `diploma.MatchFullName(subjectName, birthDate, identity)`, the DUO holder
  match (diacritics, prefixes, given-name order). A mismatch is recorded as
  `rejected` with `IDENTITY_MISMATCH` (no proofed name kept); a failed
  identity read leaves the request undecided for the next reconcile. A
  review approval is matched too. The engine still holds its approval, so
  the result read (`requestResult` → `heldToVerdict`, customer API and admin
  view alike) answers the wallet's rejection and code with no identity or
  images: the other person is never shown (`ASSURANCE_NOT_MET` likewise).
- **Shown.** `expectedSubject` on the request, the customer API's session
  and its webhooks; "Expected person" in the Sessions tab's details; the
  reason in words (`proofingRejectionReason`). A test session's identity is
  the sandbox fixture, `Sandbox Testperson` born 1990-01-01, so an
  integrator can script a match and a mismatch.
- **Not built:** the hosted and on-screen pages do not name the expected
  person to the subject.

## 14. The customer's own photo (reference photo)

For a customer that already holds a photo of the person and wants a live face
check against it without the document (an insurer confirming a policy
holder's change of bank account). IPS's `referencePhoto`, ported back.

- **Flow.** A face step without `nfc_read` (`NeedsReferencePhoto`; the
  editor's "Face check" alone). `Completable` stays false for it (a member
  has no photo to send); `CustomerCompletable` is true, so it can be
  assigned to a customer, and the flow lists carry `needsReferencePhoto`.
  It runs in the Idem app only (`YiviAppAvailable` false: the Yivi app
  matches against its credential's photo) and reaches no eIDAS level (the
  levels need the chip; the engine scores it with `chipReference` false).
  The send form leaves it out.
- **Send.** Only the customer API: `referencePhoto`, standard base64,
  sniffed as PNG/JPEG/WebP, at most `MaxReferencePhotoBytes` (512 KiB;
  the create body is capped at 1 MiB, an idempotent one past that is 413
  `body_too_large`). Required on such a flow (422
  `reference_photo_required`), refused on any other (400), so a chip flow is
  never matched against a supplied photo. A test request checks it and
  drops it (a scripted session runs no flow). Audited as `referencePhoto:
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
