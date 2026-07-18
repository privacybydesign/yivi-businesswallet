# QERDS — Qualified Electronic Registered Delivery Service

**Status:** local/dev implementation (stub provider). Not production-qualified.
**Regulation:** COM(2025) 838 Art 5(1)(i), 5(1)(m), 5(1)(n), 5(3), 6(1)(d), 6(1)(i), 6(1)(j); eIDAS (EU 910/2014) Art 43–44.
**Source refs:** `regulation/FEATURE_LIST.md`, `regulation/COM_2025_838_act.md`, `regulation/COM_2025_838_annex.md`.

---

## 1. What this is

The European Business Wallet must let owners **transmit and receive documents/data through a
qualified electronic registered delivery service** with confidentiality and integrity
(Art 5(1)(i)), keep a **transaction log** (Art 5(1)(m)), and expose a **dashboard for accessing,
storing and verifying** those communications (Art 5(1)(n)).

We do **not** become the Qualified Trust Service Provider (QTSP). `FEATURE_LIST.md` flags QERDS as
the scarcest capability (only two qualified NL providers) and central to the B2G
submission/notification obligations. The regulation requires the wallet to **integrate and support**
a designated QERDS (Annex §1), not to *be* one. So we integrate with an external qualified provider.

### The seam this mirrors

This is the same shape already solved for authentication. With Yivi, the daemon is the trust
service and our backend is the **requestor** talking to it over HTTP via `internal/irmarequestor`.
QERDS is the identical seam one level up:

```
Yivi (auth):   backend ──requestor──▶ irma daemon        (trust service)
QERDS:         backend ──client─────▶ QERDS provider API  (qualified trust service)
```

Everything learned building `irmarequestor` transfers: a thin typed client, an authenticator
interface as a config-swappable seam, a one-shot boot `Ping`, and correlate-by-provider-token
(never by our request-id — it does not exist on the provider's side).

---

## 2. Regulatory constraints that shape the design

A QERDS is not "send a message." eIDAS Art 44 requires the service to guarantee, **with legal
effect**: identification of sender and recipient before delivery, integrity protection, a
**qualified electronic timestamp** on send and on receive, and **evidence** of sending and
receiving that is tamper-evident and admissible.

Two consequences:

1. **The evidence is the product, not a side-effect.** Delivery evidence (submission, relay,
   delivery, non-delivery receipts) is what gives a message its legal effect. It is stored
   **immutable / append-only** — first-class business data, not an ops log. (Same philosophy as
   `internal/audit`, different purpose.)
2. **Recipients are resolved through a directory, not free text.** Art 6(1)(j) assigns each owner
   ≥1 **unique digital address**; Art 6(1)(i) requires an interface to the European Digital
   Directory. Sending is address→address, brokered by the provider.

Interop standards the implementing act will point at (not yet frozen — an implementing act was
flagged upcoming Sept 2025): ETSI **EN 319 522** ERDS family (REM / eDelivery), **AS4 (ebMS3)**
transport over the eDelivery network, **SMP/SML** for address resolution. Annex §1(2) mandates
open, royalty-free standards, end-to-end encryption, and mandatory interoperability — i.e. no
vendor lock-in. That is exactly why the provider is behind an interface.

---

## 3. Backend architecture

Two new packages, mirroring the auth split.

### 3.1 `internal/qerdsprovider` — external client (the `irmarequestor` analogue)

No domain logic. Talks to the partner QERDS provider. Contains:

- `Client` with the provider operations:
  - `Send(ctx, OutboundMessage) (SendReceipt, error)` — submit; returns provider ref + submission evidence.
  - `Fetch(ctx) ([]InboundMessage, error)` — poll for new inbound (fallback path).
  - `Evidence(ctx, providerRef) ([]Evidence, error)` — pull evidence for a message.
  - `ResolveAddress(ctx, identifier) (Address, error)` — directory lookup.
  - `Status(ctx, providerRef) (DeliveryStatus, error)`.
- A `RequestAuthenticator`-style seam. Providers differ (mTLS + OAuth2 client-credentials for AS4
  gateways vs bearer tokens for REST). Keep auth swappable by config, same reasoning that made
  `irmarequestor.RequestAuthenticator.Authorize(req) (body, headers, err)` body-level rather than
  header-level.
- A boot `Ping(ctx)` — resolve our own digital address / provider capability call. **Fatal** on
  failure, same as the Yivi readiness gate. Catches "will the provider accept what we'll send"
  (creds valid, our address provisioned, scheme reachable), which `depends_on` cannot.
- **Local `StubProvider`** — an in-process implementation of the same interface used in dev/test.
  Deterministic, generates plausible ERDS-style evidence with timestamps so the whole flow is
  exercisable offline. This is the QERDS equivalent of the `--no-auth` empty-token dev
  authenticator. Selected by config (`QERDS_PROVIDER=stub`).

The provider dependency is expressed as a **consumer-defined interface** in `internal/qerds`
(accept interfaces, return structs) — the concrete client/stub is injected at boot.

### 3.2 `internal/qerds` — domain slice (org-scoped)

Follows the layout conventions. This slice **needs a service** (unlike pure-CRUD `organization`)
because a send orchestrates store + provider client + evidence persistence + audit, with
cross-cutting rules — this is the `auth`-style "with-service" template.

```
internal/qerds/
  handler.go          HTTP: parse, authz-in-context, status mapping, respond.*
  service.go          orchestration: send flow, inbound intake, evidence reconciliation
  message_store.go    pgx CRUD for messages
  evidence_store.go   append-only evidence records
  address_store.go    org ↔ digital address mapping
  webhook.go          inbound push endpoint (provider → us), out-of-band auth
  status.go           delivery state-machine constants
  qerds.go            domain types + package sentinel errors
```

Routes register via the `Registerer` interface (`Register(*http.ServeMux)`) like every other slice.
Org-scoped routes compose `auth.RequireUser` → `organization.Handler.Authorize` → optional
`organization.RequireOrgAdmin`, and read the org via `organization.OrgFromContext`. Identity stays
central and slug-free.

- Sending / reading messages: member-or-admin (`Authorize`).
- Managing the org's digital address: admin (`RequireOrgAdmin`).
- The **webhook** endpoint lives **outside** the org-slug/session middleware chain (like health
  probes) and is authenticated out-of-band (HMAC signature / mTLS) — it is machine-to-machine
  (Art 6(1)(d) "automated interaction"), never session-cookie authed.

---

## 4. Data model (Postgres, goose migrations, gen_random_uuid PKs)

One logical table per migration (convention). Timestamps `timestamptz`, `camelCase` JSON at the
edge.

- **`qerds_addresses`** — `(id, org_id FK, address unique, is_default, provider_ref, provisioned_at,
  created_at)`. Art 6(1)(j) allows ≥1 address per owner; model as a set from day one.
- **`qerds_messages`** — `(id, org_id FK, direction ['outbound'|'inbound'], sender_address,
  recipient_address, subject, provider_ref unique, status, submitted_at, delivered_at,
  qualified_timestamp_send, created_at, updated_at)`. `provider_ref` is the correlation key into
  the provider — our request-id never exists on their side.
- **`qerds_attachments`** — `(id, message_id FK, filename, content_type, content_hash, size_bytes,
  content, storage_ref)`. Content is large and possibly E2E-encrypted ciphertext we cannot read —
  the store is **content-opaque**; the row holds the hash + integrity metadata. **Current MVP: bytes
  live inline in the `content` BYTEA column** (blob-column). `storage_ref` is reserved for a later
  object-storage backend (bytes elsewhere, referenced by the column) — the swap stays a code change
  behind the store interface, not a schema break. Uploaded over multipart on `POST .../messages`,
  streamed back via `GET .../messages/{id}/attachments/{attachmentId}` (org-scoped by join). Per-file
  / per-message / count limits are enforced in the handler.
- **`qerds_evidence`** — **append-only**. `(id, message_id FK, evidence_type, provider_ref,
  qualified_timestamp, raw_evidence bytea, verified_at, created_at)`. Never updated, only inserted.
  Backs the Art 5(1)(n) "access, store and verify" dashboard.

### Delivery state machine (`status.go`)

Outbound: `draft → submitted → accepted → delivered → read` with terminal `failed` / `expired`.
Each transition is ideally anchored to a piece of evidence carrying its qualified timestamp.
Inbound: `received → read`. Transitions are idempotent (webhooks retry; dedupe on `provider_ref`).

---

## 5. Flows

### 5.1 Outbound (send) — asynchronous

1. Handler validates + persists the message (`submitted`) and audits, in one transaction
   (`database.InTx` + `audit.Record` on the same `q`).
2. Service resolves the recipient via the provider directory (`ResolveAddress`).
3. Service calls `qerdsprovider.Send`; stores the returned `provider_ref` + submission evidence.
4. Returns `submitted` immediately. **Do not block on end-to-end delivery** — QERDS delivery can
   take minutes to days. Evidence arrives later (webhook or poll) and advances the state machine.

If the provider call fails after the DB commit, the message sits in a retryable state — not lost.

### 5.2 Inbound (receive) — webhook preferred, poll fallback

- **Webhook push (preferred):** provider POSTs to `webhook.go`, authenticated out-of-band
  (HMAC/mTLS). Records the inbound message + delivery evidence (that evidence itself has legal
  effect — it proves *we* received it). Idempotent on `provider_ref`.
- **Polling fallback:** a background worker pulls new messages by address (a `cmd/` binary or a
  ticker, consistent with the migrate/seed-as-separate-service pattern). Simpler, no inbound
  network exposure, higher latency.

---

## 6. Standalone-service mode (Art 5(3))

The regulation **mandates** offering QERDS as a standalone service to EUDI-wallet users (sole
traders) without a full Business Wallet. The architecture already leans this way: the `qerds` slice
depends only on an *org* + a *digital address*, not the rest of the wallet. Keep that dependency
thin so a lightweight single-user org is a valid QERDS-only tenant. The send path must not reach
into unrelated wallet features.

---

## 7. Frontend

`src/api/` resource client + query hooks; `src/routes/` for inbox/outbox list, message detail with
an **evidence panel** (the "verify" half of Art 5(1)(n) — show qualified timestamps + the receipt
chain, allow export/validate), a compose flow with directory-backed recipient lookup and an
attachment picker (multipart upload, client-side size/count limits mirroring the handler), an
attachment list on the detail view with credentialed download, and address management in org
settings.

---

## 8. Config & deploy

Env-driven like everything else (`internal/config`):

| Var | Meaning | Required |
|---|---|---|
| `QERDS_PROVIDER` | `stub` (dev) or the provider driver name | no (default `stub`) |
| `QERDS_PROVIDER_URL` | provider base URL | when not `stub` |
| `QERDS_AUTH_TOKEN` | bearer / creds material | when the driver needs it |
| `QERDS_WEBHOOK_SECRET` | HMAC secret for inbound webhook auth | when webhook enabled |
| `QERDS_DEFAULT_ADDRESS_DOMAIN` | domain for minted digital addresses | no (has default) |

Boot `Ping` is **fatal** (matches the Yivi readiness gate). `/readyz` stays DB-only.

---

## 9. Local test bench

There is **no single "QERDS-in-a-box"**. The standard splits into two layers, each with a real
bench:

- **Transport (AS4 / eDelivery):** the EU **eDelivery Conformance Testing Service** (GITB testbed)
  + **Domibus**, the Commission's open-source AS4 access point (self-hostable, self-test mode). This
  is a real local fake for the *plumbing*.
- **Evidence (ERDS/REM):** ETSI **EN 319 522 / 532**, conformance per **TS 119 524**, exercised at
  periodic **ETSI Plugtests** — no permanent public sandbox. This is why we validate the qualified
  evidence layer against a **partner QTSP sandbox**, not a generic one.

### Tiered strategy

```
Dev/CI:   qerdsprovider StubProvider (in-process)   ← offline, deterministic, default
   ↕      Domibus in Compose (AS4, `domibus` profile) ← proves transport plumbing
Staging:  partner QTSP sandbox                        ← real ERDS evidence + qualified timestamps
Prod:     partner QTSP production
```

Same three-tier shape as `irma-demo.*` → `pbdf.*`.

**Caveat:** the Stub/Domibus loop proves plumbing, **not** compliance. A green local loop must not
read as "QERDS done" — the evidence store and qualified-timestamp capture are only truly exercised
against a real QTSP sandbox.

### Domibus AS4 bench (opt-in dev Compose)

`compose.override.yaml` defines `domibus` + `domibus-mysql` (the EC/FIWARE reference images,
Domibus 4.0) behind a `domibus` **profile** so the default `npm run dev` is unaffected. Bring it up
with:

```
docker compose --profile domibus up -d       # Tomcat is slow under arm64 emulation
```

Admin console: `http://localhost:8090/domibus` (`admin` / `123456`). Point the backend at it with:

```
QERDS_PROVIDER=domibus
QERDS_PROVIDER_URL=http://domibus:8080/domibus/services/backend
```

The `qerdsprovider.DomibusProvider` driver speaks the WS-plugin SOAP (submitMessage /
listPendingMessages / retrieveMessage) and boot-probes the endpoint's WSDL. Its ebMS3 addressing
(`QERDS_DOMIBUS_*`) defaults to the Domibus **sample PMode** parties (`domibus-blue` → `domibus-red`,
service `bdx:noprocess`, action `TC1Leg1`). A different Domibus deployment needs those vars aligned
to its PMode.

**Live verification (manual, against this bench):** `Ping` succeeds against the real WS-plugin WSDL,
and `submitMessage` is **structurally accepted** by Domibus 4.0 — the envelope unmarshals fully
(this shook out a real bug: the WS-plugin `payload`/`value` elements are `elementFormDefault`
unqualified and must reset to the empty namespace inside `submitRequest`, now covered by a
regression test). A fully-*accepted* submission additionally requires a **PMode uploaded to the
Domibus instance** (a fresh Domibus answers `EBMS:0010 PMode could not be found`); that is a
Domibus-admin step (upload the sample PMode via the `:8090` console), not driver work. CI exercises
only the envelope construction + response parsing (unit tests); the **stub remains the verified
default** provider.

### What this branch implements

- **Backend:** `qerdsprovider` interface + `StubProvider` + `DomibusProvider`, the `qerds` domain
  slice, migrations, org-scoped routes, boot `Ping`, and the inbound webhook.
- **Frontend:** inbox/outbox list, message detail with the delivery-evidence panel, compose flow,
  and digital-address management (`src/api/qerds*`, `src/routes/qerds*`).
- **Dev bench:** the Domibus `domibus`-profile Compose services above.

Attachments are implemented end-to-end (multipart upload, content-opaque blob-column storage,
org-scoped download; threaded through the provider seam — stub loopback + Domibus ebMS3 payloads).

Follow-ups: the partner QTSP driver + sandbox integration, object-storage backend for attachments
(behind the unchanged store interface), inbound Domibus multi-payload extraction, and the European
Digital Directory (SMP/SML) lookup.

---

## 10. Address resolution & the interim address book

The recipient's digital address is discovered, in the target state, via the **European
Digital Directory** (COM(2025) 838 Art 10) — a Commission-run registry keyed on the owner's
**EUID** (BRIS / Company Law Directive 2017/1132), resolved through eDelivery-style dynamic discovery
(SMP/SML). That directory does not exist yet (specs pending implementing acts), so `ResolveAddress`
stays an abstract seam and, in the meantime, the wallet uses:

- **A per-org address book (`qerds_contacts`)** — save recipients (name → digital address) and reuse
  them in Compose via a datalist picker. This is the interim stand-in for the directory.
- **The partner QTSP's own addressing + REM/eDelivery interoperability** for actual routing.

When the European Digital Directory arrives it becomes one more resolver behind `ResolveAddress`
(explicit address → contacts → directory), with no domain/UI churn. Reachability during the
transition is limited to recipients already on an interoperating registered-delivery network — the
UI must not imply EU-wide coverage that doesn't exist yet.

## 11. Open questions / follow-ups

- Which NL QTSP to partner with (drives the concrete `qerdsprovider` driver + auth shape).
- Attachment storage backend: shipped as a blob column (MVP); revisit object storage (`storage_ref`)
  once real payloads / E2E ciphertext land and the inline-BYTEA footprint becomes a concern.
- European Digital Directory (SMP/SML) integration for cross-border address resolution (replaces the
  interim contacts address book as the primary resolver).
- Multi-replica inbound: webhook idempotency store vs the daemon-style single-replica assumption.
- Frontend evidence-verification UX (validate qualified timestamps client-side vs server-side).
