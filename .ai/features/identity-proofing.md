# Feature: Identity proofing (identity-proofing-service)

**Status:** Integrated (backend + frontend). Every org gets its own IPS tenant on
first use. The sidebar's "Identity proofing" section holds three pages under
`/{org}/identity-proofing`: **Overview** (the last 30 days' customer sessions
counted, the newest ones, the customers, and an alert per failing webhook),
**Customers** (search, 30-day sessions and verified share, webhook health,
status) and, admin-only, **Flows** (full IPS configuration, versioned, and which
flows members may use). A customer's page has tabs: Flows and Sessions for every
member; Branding, API keys, Webhooks and Settings for an admin. "Verify a
person" in its header opens the send form; an admin can pause a customer, after
which no request can be sent for it (`customer_paused`, 409) while sent ones run
out. The customers API stays at `/orgs/{slug}/customers`. A member is sent a
request from their member detail page (admin-only, like that page). Customers
are B2B clients with no login: members act for them, and their own backend can
through the public API with one of their keys. Sending creates an IPS session
(2, 5 or 10 minutes, per customer) and mails its vcmrtd deep link as a QR code
and a button: the mail is the session, with no wallet page in between. The
outcome lands on the request, in the audit log and at the customer's webhook.
Not built from the design: the hosted flow (and with it "Open hosted flow",
allowed return URLs and the consent-screen preview; Branding previews the mail
instead), test-mode keys and the sandbox, and the method column (every session
runs in vcmrtd).
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
(customers, tabs, assignment, send, requests, pause). There is no public recipient page:
the mail is the session.
**Depends on:** `privacybydesign/identity-proofing-service` ("IPS"), an HTTP
service we are a relying party of. It is itself work in progress: its
device-binding API (claims, handover, `/submit`) was uncommitted on its
`Compliance` branch when this was built. Every IPS wire detail lives in
`internal/proofingprovider/client.go`, so an IPS change is a one-file change.

---

## 1. Model

- **One org = one IPS tenant, provisioned on first use.** The wallet holds the
  deployment's IPS `ADMIN_KEY` (`IDENTITY_PROOFING_ADMIN_KEY`); no admin ever
  enters a key. The first org route that needs the org's key (listing or creating
  flows, saving the selection, sending a request) creates the tenant
  (`POST /api/v1/admin/tenants`) and a key scoped to `sessions:*` + `flows:*`, and
  stores both the key and the webhook secret sealed under
  `IDENTITY_PROOFING_ENCRYPTION_KEY` (`org_identity_proofing_settings`), audited
  `identity_proofing.provisioned`. IPS shows both exactly once. Provisioning checks
  the encryption key exists *before* calling IPS, so a missing key never leaves an
  orphaned tenant. Two concurrent first uses can leave one unused tenant at IPS;
  it is logged, not prevented. There is no enable step or on/off state.
- **Customers are the wallet's, not IPS sub-tenants.** IPS sub-tenants only
  override privacy knobs (BSN policy, blurring), share the parent's keys and
  cannot own flows, so a customer (`identity_proofing_customers`) is a wallet row
  under the org and every session still runs on the org's IPS tenant. A customer
  has no login and no API key: members act for it (the design plan's
  "org is the only party in the UI"). Flows stay the org's; a customer gets an
  allow-list over them with one default (`identity_proofing_customer_flows`,
  audited `customer_flows_configured` with before/after), independent of the
  members' list: any completable org flow may be assigned. Customers are created,
  renamed and paused or resumed (`customer_created`; `customer_updated` with the
  `name` or `status` before and after; `paused_at` on the row), never deleted: a
  request references its customer `ON DELETE RESTRICT`. This deliberately differs from
  `.ai/plans/identity-proofing.md`, where each customer owns its flows.
- **Flows live at IPS**, versioned, managed like IPS's own admin page
  (`/api/v1/db-test/admin`): that page, not the tenant API docs, is the editor
  the wallet's "Proofing flows" tab mirrors. It calls `/api/v1/flows` with the
  org's tenant key (IPS has no admin-key flow routes), and it is `RequireOrgAdmin`
  in the wallet. The rules copy IPS (`flow.Validate` + the admin page's
  `syncStageDependencies`):
  - Steps: `document_capture` (vcmrtd scans the MRZ to unlock the chip) and
    `nfc_read` (NFC chip read) toggle as a pair; `face_verification` is one step.
  - Checks: `nfc.passive_auth` is locked on with `nfc_read`, and `face.match`
    with `face_verification`; `nfc.chip_auth` and `face.liveness` are optional;
    a check without its step is unavailable; the threshold is only for
    `face.match`.
  - Requested data: `dg1` comes from the document scan; `dg11`, `dg2` and
    `chip_checks` from NFC; `selfie` and `biometrics` from face. Each item is
    available only with its step and cleared otherwise. None is forced on (IPS
    forces nothing).
  - Assurance level: none/low/substantial/high, as IPS offers; IPS refuses high
    and an unbacked substantial, and the wallet shows its message.
  - Overrides: BSN policy, blur face and blur BSN (inherit/true/false),
    retention in seconds.
  - Legal basis, purpose and assurance tiers are not on IPS's editor, so not on
    the wallet's: they are carried over unchanged when a version is saved.
  Face capture is forced to `native`. A face step without `nfc_read` needs a
  per-session reference photo the wallet does not have, so such a flow is not
  `completable` and cannot be made available or sent. "Edit" is
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
- **Recipients: a member, or a customer's subject.** A request names either a
  `userId` of the org (`subject_user_id`; any role, employees and externals
  alike) on a flow members may use, or a `customerId` plus an e-mail address and
  an optional name (a CHECK keeps the two exclusive) on a flow assigned to that
  customer (`flow_not_assigned` otherwise). A subject is never a user or member.
  Name and address are a snapshot; `subject_name` is `''` when none was given.
  The mail does not name the customer yet.
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
own expiry; raised from 5 for this), so the mailed QR works for the whole
session.

**Send.** `CreateRequest` creates the IPS session (`ttlSeconds` = 600,
`proofing.SessionTTL`, client reference = the request id), stores the request
with `link_expires_at` = the session's expiry, attaches the session
(`AttachSession`, audited `identity_proofing.session_created`; `flow_version`
becomes the version IPS pinned), and mails the create response's native claim:
the `qr` block and the button carry the **same** vcmrtd deep link, and the text
states `validMinutes`. A session with no native claim is an error and nothing is
stored. A session whose request then fails to store lapses unused at IPS. Scan
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

## 3. Outcomes: poll-on-read, no webhook

The request list reconciles against
`GET /api/v1/sessions/{id}/result` (the list re-checks at most
`maxReconcilePerList` live rows per read). The IPS webhook is **not** used: it
goes to a per-session `callbackUrl` and carries the full result, including the
document fields and images. `needs_review` is not final: it keeps being
reconciled. IPS `opened`/`in_progress` moves the request from `pending` to
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

**Background reconciler.** `ReconcileLive` re-checks up to 50 live requests
across every org each minute, so an outcome or expiry lands (and its webhook is
sent) with nobody reading a list. Every IPS result read is audited at IPS, which
is why it is not faster.

**Method and timeline.** `proofingprovider` derives the app a subject used
from the `/result` answer, reading only each device's `role` and whether a
Yivi `disclosure` exists: `yivi_app` (a Yivi disclosure), `idem_app` (IPS's
native device; vcmrtd is the Idem app), `browser` (the web device alone), or
none while no device claimed the session. It is stored on the request
(`method`) by `MarkStarted`, `RecordOutcome` and `EndSession`, and carried in
their audit snapshots. `GET /identity-proofing/requests/{id}/events` is the
request's timeline: its audit events (target `identity_proofing_request`),
oldest first, for an admin or the member who sent it. Everything `reconcile`
records runs under `audit.WithoutActor`: the outcome is the subject's doing,
not that of whoever's read triggered the check (rows written before this
still name the reader).

## 3a. Customer API, webhooks, branding

- **API keys** (`identity_proofing_api_keys`): `yp_live_` + 32 random bytes,
  stored as SHA-256 only, shown once; `prefix` tells keys apart. Revoke is
  permanent; removing the customer removes them. Audited `api_key_created` /
  `api_key_revoked` on the customer. There is no test mode.
- **Public API** (`/api/v1/proofing/{flows,sessions,sessions/{id}}`, Bearer
  key, no cookie): acts exactly as a member sending for the customer, on its
  assigned flows (no `flowId` is its default), refused while paused. `sendMail:
  false` skips the mail; the create answer always carries the `deepLink`. A
  request made this way has `requested_by` NULL and `api_key_id` set, and every
  mail for a customer's subject names the customer as requester, never the key.
  No rate limit of our own (IPS limits session creation per IP).
- **Webhooks**: one endpoint per customer (`identity_proofing_webhooks`, secret
  sealed with the proofing key, `whsec_…`, shown once). Events
  `session.verified` / `.failed` / `.expired` / `.purged` are written to the
  outbox (`identity_proofing_webhook_deliveries`) in the same transaction as the
  change (`RecordOutcome`, `EndSession`, `PurgeProofedNames`); `test` is sent on
  request whatever is subscribed. The payload is the session id, status, flow and
  assurance: never a name or address (the API has those). The deliverer runs
  every 10 s, leases due rows (`FOR UPDATE SKIP LOCKED`, 5-minute lease), POSTs
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
  (7/30/90, the proofed-name retention). The frontend options are held to the Go
  lists by `lib/identity-proofing.test.ts`.
- **Remove customer** deletes its requests (addresses, names, outcomes), keys,
  endpoint and deliveries; the audit trail stays (`customer_removed`).

## 4. Data minimisation

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
`proofedName`, never audited, and cleared by the `identity_proofing_proofed_names`
pruner after `proofing.ProofedNameRetention` (30 days). A member's request, or a
rejected or `needs_review` one, keeps no name.

## 5. Known IPS gaps to track

- **No end-user web page.** IPS's web claim URL points at its staff-only db-test
  page. So only flows whose face capture runs in the app (`selfieLocation:
  native`) can be sent. The wallet forces `native` on flows it creates (IPS
  defaults to `browser`) and refuses requests on other flows
  (`flow_not_completable`).
- **Deep link tap does not open vcmrtd yet** (IPS `session-model.md`); scanning
  the QR from inside the app works.
- **Every result read is audited at IPS** as `proofing.data.read` and returns the
  full result. A lightweight status endpoint at IPS would make polling cheap.
- **IPS rate-limits session creation per client IP** (30/min), shared by every
  org of a wallet deployment behind one egress IP.
- Local dev: IPS and the wallet backend both publish host port 8080; remap one
  (see `compose.yaml`).
