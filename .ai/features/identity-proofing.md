# Feature: Identity proofing (identity-proofing-service)

**Status:** Integrated (backend + frontend). Every org gets its own IPS tenant on
first use. An admin defines flows (full IPS configuration, versioned) on the
"Proofing flows" tab and picks which ones members may use. The "Identity
proofing" tab lists every member of the org (admins and externals included) with
a flow picker and a mail button. The mail carries a QR code and a button for one
15-minute link, and the outcome lands on the request and in the audit log.
**Slice:** `internal/proofingprovider` (the IPS client + stub, leaf level),
`internal/proofing` (settings, flow selection, members, requests, service,
handler), `internal/email` (kind `identity_proofing_requested`, the `qr` block),
`frontend/src/routes/identity-proofing.tsx` (members + requests),
`frontend/src/routes/identity-proofing-flows.tsx` (admin: flow editor, versions,
selection) and the public `frontend/src/routes/proof.tsx`.
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
- **Sub-tenants: not used.** IPS sub-tenants only override privacy knobs (BSN
  policy, blurring), share the parent's keys and cannot own flows. The wallet has
  departments but no sub-orgs. Revisit when IPS scopes flows per sub-tenant.
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
- **Recipients are members only.** A request names a `userId` of the org
  (`subject_user_id`): any role, employees and externals of this org alike; the
  name and address are a snapshot. No free-form recipients.
- **Roles:** any member lists members and allowed flows, sends requests, and
  sees the requests they sent; creating and editing flows, versions and the
  selection are `RequireOrgAdmin`; an admin lists every flow (with
  `allowed`/`default`) and every request of the org.

## 2. One lifetime: the mail, the link and the session

IPS caps a session at 15 minutes (`SessionMaxLifetime`, hardcoded) and its claim
(the `handover` token in the QR/deep link) at 5 minutes, single use. The wallet
therefore creates the IPS session **when the mail is sent** (`ttlSeconds` = 900)
and stores the request with `link_expires_at` = the session's expiry, never more
than `proofing.SessionTTL`. The mail's `qr` block and its button carry the **same**
wallet link, `/proof/{token}` (token hashed at rest), so both do the same thing
and expire together. That page shows IPS's `vcmrtd://verify?handover=…&api=…`
deep link as both a QR code and an open-in-app link (one payload), and re-mints
the claim via `/claim-tokens` when it lapses, while the session lives. An IPS
session that ends early (expired or cancelled) ends the link with it
(`ExpireLink`). There is no restart: a new request means a new mail. The `api`
host is IPS's `PUBLIC_BASE_URL`, which must be reachable from the phone. A mail
that fails to send leaves the request standing but answers `mailSent: false`, and
the UI says so.

## 3. Outcomes: poll-on-read, no webhook

The public page and the request list reconcile against
`GET /api/v1/sessions/{id}/result` (the list re-checks at most
`maxReconcilePerList` live rows per read). The IPS webhook is **not** used: it
goes to a per-session `callbackUrl` and carries the full result, including the
document fields and images. `needs_review` is not final: it keeps being
reconciled. IPS `opened`/`in_progress` moves the request from `pending` to
`in_progress` (audited `session_started`). `expired` is
derived from `link_expires_at`, never stored. An IPS failure during a read is
logged and the last known status is shown.

## 4. Data minimisation

Stored and audited: status, IPS assurance tier, achieved eIDAS level, IPS error
code. Never the document fields, BSN or images. `proofingprovider` does not
decode them. Its decoder reads the result into a struct holding only
`status/errorCode/completedAt/result.assurance`.

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
