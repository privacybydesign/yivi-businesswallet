# Feature: Identity proofing (identity-proofing-service)

**Status:** Integrated (backend + frontend). Every org is an IPS tenant under the
org's own id, created on first use. The sidebar's "Identity proofing" section holds three pages under
`/{org}/identity-proofing`: **Overview** (the last 30 days' customer sessions
counted, the newest ones, the customers, and an alert per failing webhook),
**Customers** (search, 30-day sessions and verified share, webhook health,
status) and, admin-only, **Flows** (full IPS configuration, versioned, and which
flows members may use). A customer's page has tabs: Flows and Sessions for every
member; Branding, API keys, Webhooks and Settings for an admin. "Verify a
person" in its header opens the send form; an admin can pause a customer, after
which no request can be sent for it (`customer_paused`, 409) while sent ones run
out. The customers API is at `/orgs/{slug}/customers`. A member is sent a
request from their member detail page (admin-only, like that page). Customers
are B2B clients with no login: members act for them, and their own backend can
through the public API with one of their keys. Sending creates an IPS session
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
`yivi/disclosure`, which on DONE hands the photo and claims to IPS as the face
reference (`POST /api/v1/sessions/{id}/reference`, IPS's
`BOUND_LOGIN_RELYING_PARTY_REFERENCE` must be on; IPS's own IRMA requestor is not
used), then runs the face check from the browser camera (`yivi/face`). A subject
who cancels in the app is not reported: the verifier only knows pending or done,
so the page waits out the countdown. Both count down to the
session's expiry and poll `GET requests/{id}` for the outcome. Nothing is
mailed, and the subject's name and address travel in router state, not the URL.
The hosted page's completion (redirect origins, decline, locale) is in §7.
**Slice:** `internal/proofingprovider` (the IPS client + stub, leaf level),
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
`routes/flow-hosted-settings.tsx` (per-flow hosted settings).
**Depends on:** `privacybydesign/identity-proofing-service` ("IPS"), an HTTP
service we are a relying party of. It is itself work in progress. Every IPS wire detail lives in
`internal/proofingprovider/client.go`, so an IPS change is a one-file change.

---

## 1. Model

- **The org is the IPS tenant: one tenant id, the org's.** IPS keeps its own
  tenant model so it can run on its own, but the wallet creates each org's
  tenant under `organizations.id` (`POST /api/v1/admin/tenants` with `id`) and
  stores no IPS tenant id. The wallet holds the deployment's IPS `ADMIN_KEY`
  (`IDENTITY_PROOFING_ADMIN_KEY`); no admin ever enters a key. The first org
  route that needs the org's key (listing or creating flows, saving the
  selection, sending a request) creates the tenant with a `live` and a `test` key
  scoped to `sessions:read`/`:write` + `flows:read`/`:manage`, and stores both keys and the webhook secret
  sealed under `IDENTITY_PROOFING_ENCRYPTION_KEY` (`org_identity_proofing_settings`,
  one row per org), audited `identity_proofing.provisioned`. IPS shows them
  exactly once. Provisioning checks the encryption key exists *before* calling
  IPS, so a missing key never leaves an orphaned tenant, and runs under a per-org
  advisory lock, so concurrent first uses create it once. A tenant IPS already
  has (a first use whose save was lost) answers 409 and is taken over: the
  wallet rotates its webhook secret (`POST .../tenants/{id}/webhook-secret`) and
  mints fresh keys. There is no enable step or on/off state.
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
    alone; `face_verification` is one step.
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
  - Assurance level: none/low/substantial/high, as IPS offers; IPS refuses high
    and an unbacked substantial, and the wallet shows its message. IPS reports
    substantial only for a chip read (passive + chip auth) plus a Regula face
    check against the chip's DG2, so the wallet itself refuses a substantial
    flow without `nfc_read`, a face step and Regula (`checkReachable`), which
    IPS's own check lets through. In a session the Idem app follows the
    flow, never its own settings: Active Authentication exactly when the
    flow lists `nfc.chip_auth`, active liveness when it lists
    `face.liveness` (IPS's app view carries `requiredChecks`), and the
    face engine the flow's provider names (`faceProvider`: Regula, or the
    on-device engine for `engine`). A skipped check scores as failed and
    caps the level. IPS only
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
  per-session reference photo the wallet does not have, so such a flow is not
  `completable` and cannot be made available or sent. Neither is a flow with
  `document_photo` until the Idem app can take it (`appPendingSteps`: drop
  the step from that list once it ships); it also rules out the Yivi app. "Edit" is
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

The `api` host in the deep link is IPS's `PUBLIC_BASE_URL`, which must be
reachable from the phone. A mail that fails to send leaves the request standing
but answers `mailSent: false`, and the UI says so.

Mail goes through the org's own SMTP settings (`internal/email`); in the dev
stack the seeded org points at Mailpit (`localhost:8025`), so nothing leaves the
machine until an admin sets a real server under e-mail settings.

## 3. Outcomes: pushed by IPS, never polled

Every session is created with `callbackUrl` = `IDENTITY_PROOFING_CALLBACK_URL`
(default `APP_BASE_URL` + `/api/v1/identity-proofing/ips-events`) and
`callbackPayload: "minimal"`: IPS pushes each change (opened, in_progress,
verified, rejected, needs_review, cancelled, expired) as a notice with no
personal data, signed with the org's IPS tenant webhook secret (the event's
`tenantId` is the org id)
(`X-Signature: sha256=hex(HMAC("<X-Webhook-Timestamp>.<body>"))`, 5-minute
skew). `HandleIPSEvent` verifies it, then reconciles that one request with the
org's key (`tryReconcile`, reading `GET /api/v1/sessions/{id}/status`, §11);
the result never travels in the push. A session nobody finishes is reconciled at its cap by the deadline
job (`ReconcileDue`), which sleeps until the earliest cap and is woken by
`pg_notify` on `identity_proofing_sessions` when a session is attached; one IPS
still reports open past its cap is asked again after 30 s. The job leases what
it re-checks (`ips_reconcile_leased_until`, `FOR UPDATE SKIP LOCKED`), so API
replicas never ask IPS about the same session at once. Nothing runs on a fixed
interval. List reads never call IPS; a single-request read (the on-screen page,
the customer API's `GET sessions/{id}`) re-checks a live request at most once
per `readReconcileEvery` (10 s), as a fallback for a missed push. An event for
an unknown tenant or session is acknowledged, except a fresh one (under
`earlyEventWindow`) naming a request, which answers 503 so IPS retries it while
the session is still being stored. `needs_review` is not final: IPS decides it,
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
`POST /proofing/sessions` runs on the org's own IPS tenant under the org's IPS
**test key**, which IPS treats as that tenant's sandbox: it only creates
scripted-outcome sessions, and a live key never can. The session is created
without a flow and resolves at once to `scriptedOutcome` (`approve` by default,
`reject:<CODE>`, `needs_review`, `expire`; IPS's own sandbox): the wallet still
checks the flow is assigned to the customer and records it on the request. A
test request (`mode = test`) is reconciled right away, never mailed, has no
deep link, is left out of the stats, and carries `livemode: false` in the API
answer and its webhooks. A live key sending `scriptedOutcome` is refused
(400). Its IPS events come from the org's one tenant; the request's mode picks
the key it is reconciled with.

## 6. A new Idem code mid-session

`POST …/requests/{id}/claim-link` and `POST /proof/{token}/claim-link` call IPS
`POST /sessions/{id}/handover`. An unclaimed slot gets a new claim (the first
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
with an error code), with a required reason. `POST .../requests/{id}/review` calls IPS's
`POST /sessions/{id}/decision` (the admin's e-mail as `reviewer`), audits
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
above, erased at IPS, then its personal data cleared here. The dashboard shows
the time as `purgeAt`. A member's request is not purged.

## 11. Known IPS gaps to track

- **No end-user web page.** IPS's web claim URL points at its staff-only db-test
  page. So only flows whose face capture runs in the app (`selfieLocation:
  native`) can be sent. The wallet forces `native` on flows it creates (IPS
  defaults to `browser`) and refuses requests on other flows
  (`flow_not_completable`).
- **Deep link tap does not open vcmrtd yet** (IPS `session-model.md`); scanning
  the QR from inside the app works.
- **Reconciling reads `GET /sessions/{id}/status`**, which carries no personal
  data and is not audited at IPS; the full `/result` (audited as
  `proofing.data.read`) is read only for an approved customer subject, whose
  name is kept. Against an IPS without the status route the client falls back
  to `/result`.
- **IPS rate-limits session creation per tenant** (30/min by default), so each
  org has its own budget.
- Local dev: IPS and the wallet backend both publish host port 8080; remap one
  (see `compose.yaml`).
