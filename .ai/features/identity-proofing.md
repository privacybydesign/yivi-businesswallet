# Feature: Identity proofing (identity-proofing-service)

**Status:** Integrated (backend + frontend). Every org gets its own IPS tenant on
first use. An admin defines flows (full IPS configuration, versioned) on the
"Proofing flows" tab and picks which ones members may use. A member is sent a
request from their member detail page (admin-only, like that page): a flow
picker over those flows, a send button and the last request's status. There is
no separate members tab. "Customers" is its own top-level page
(`/{org}/customers`, API `/orgs/{slug}/customers`), not a part of proofing: it
lists the org's customers (B2B clients, no login); an admin assigns each a
subset of the org's flows, and any member verifies an external person for a
customer by e-mail address and an optional name. Sending creates a 10-minute
IPS session and mails its vcmrtd deep link as a QR code and a button: the mail
is the session, with no wallet page in between. The outcome lands on the
request and in the audit log.
**Slice:** `internal/proofingprovider` (the IPS client + stub, leaf level),
`internal/proofing` (settings, flow selection, members, requests, service,
handler), `internal/email` (kind `identity_proofing_requested`, the `qr` block),
`frontend/src/routes/member-proofing.tsx` (the member detail panel),
`frontend/src/routes/identity-proofing-flows.tsx` (admin: flow editor, versions,
selection), `frontend/src/routes/customers.tsx` + `customer-detail.tsx`
(customers, assignment, send, requests). There is no public recipient page:
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
  members' list: any completable org flow may be assigned. Customers are created
  and renamed (`customer_created`/`customer_updated`), never deleted: a request
  references its customer `ON DELETE RESTRICT`. This deliberately differs from
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
