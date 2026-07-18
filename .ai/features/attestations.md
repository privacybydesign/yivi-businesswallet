# Feature: Attestations (EAA issuance & holding for Business Wallets)

**Status:** Issue side **implemented (v1)**. Holder side: Phase 1 (held index + Received
tab) and Phase 2 (real irmago EUDI holder engine, `internal/eudiholder`) **implemented**
(Phase 2 pending irmago#622 merge — see `.ai/plans/feat-attestations-holder.md`); receive
flows (Phase 3) and export (Phase 4) still **Design**. The design
surface is issue #15 ("Attestations for Business wallets") — the `Attestations`
screen (Templates / Issued / Schemas tabs) and the "Issue attestation" wizard
(template → recipient → attributes → review).

**Built in v1 (issue side):** the `internal/attestation` slice (schemas / templates /
keys / issuance ledger), the `internal/openid4vciissuer` provider seam
(`VeramoIssuer` targeting `veramo-issuer.openid4vc.staging.yivi.app` +
`StubIssuer`, stub the verified default), migrations, audit actions/targets, config
(`ATTESTATION_ISSUER*`), boot `Ping`, seed, and the frontend (three tabs + issue
wizard with QR/`offerUri` + poll). Store + service + HTTP integration tests pass
against a real Postgres; the Veramo endpoint is reachable (create-offer needs
`ATTESTATION_ISSUER_ADMIN_TOKEN`).

**Built in v1.1 (recipient delivery + subject type):** a schema declares a
**`subject_type`** (`natural_person` | `organization`) that drives both the wizard
recipient step and the delivery route. On issue, the created offer is persisted
(`offer_uri`, `tx_code`, opaque `claim_token`) and delivered to the recipient:
- **natural person → e-mail.** New `internal/email` slice (per-org SMTP settings,
  password encrypted at rest via `internal/crypto` under `EMAIL_ENCRYPTION_KEY`) +
  `internal/mailer` (net/smtp transport). Dev uses **Mailpit** (`compose.override`,
  UI :8025); the seeder points the demo org's SMTP at it. Managed in Org Settings.
- **organization → QERDS.** The offer is sent as a QERDS message to the recipient's
  digital address, picked from the QERDS **address book** (`qerds_contacts`).
- Both link to a **public claim page** (`GET /api/v1/attestations/claim/{token}`,
  unauthenticated, opaque-token-keyed) that renders the QR + open-wallet link and
  polls to `claimed`. Delivery failures are non-fatal (the issuing UI still shows the
  QR). Delivery routing + the claim flow are integration-tested.

**Divergences from the v1 design below (deliberate):**
- **Schema→issuer mapping** is an explicit `credential_config_id` column on
  `attestation_schemas` (the Veramo `credentialId`), not derived — §6.1 reflects this.
- **Localized display metadata is provisioned into the issuer by GitOps, not at
  runtime.** A schema carries per-language display for the credential
  (`attestation_schemas.display`, `[{lang,name}]`) and each attribute
  (`attributes[].display`, `[{lang,label}]`). These are **not** sent on the
  credential offer — in OpenID4VCI/SD-JWT VC `display` lives on the credential
  *type*, served from the issuer's metadata (`credential_configurations_supported[<id>].credential_metadata.display`
  and `.claims[].display`), keyed by `credentialConfigId`. The hosted Veramo issuer
  *has* an admin API to register credential configs (`PUT/POST /api/credentials`,
  gated by a global `BEARER_TOKEN`), **but that token is not set on the deployment**
  (`openid4vc-poc-ops/veramo-issuer.tf`), so the runtime config API is disabled;
  display is provisioned by static files (`conf/metadata/<instance>.json` +
  `conf/vct/*.json`) rolled out via a ConfigMap. We therefore **generate** the
  drop-in config from a schema rather than push it: `GET .../attestations/schemas/{id}/issuer-config`
  (admin) returns the `credential_configurations_supported` fragment + a VCT
  document (`internal/attestation/issuerconfig.go`, pure `BuildIssuerConfig`); the
  schema editor shows both with copy buttons. An operator commits them to
  `openid4vc-poc-ops` and redeploys. This partially answers **Open Question #1**
  (how an org's `credentialId` display is registered on the instance): today, via
  the generated GitOps fragment. If the issuer's `BEARER_TOKEN` is ever enabled,
  the same `BuildIssuerConfig` mapping can drive a runtime `PUT/POST /api/credentials`
  push instead.
- **Issuer instance is per-organization.** Each org has its own Veramo issuer
  instance (the `{instance}` path segment): a new `org_issuer_settings` slice
  (`internal/issuersettings`, org settings → **Issuer** tab) stores the instance
  name (default = org slug) + display-name/logo branding — **no secret**, since the
  hosted issuer's admin token is deployment-global (every instance renders the same
  `VERAMO_ISSUER_ADMIN_TOKEN`). `openid4vciissuer.OfferRequest.Instance` /
  `Status(instance, …)` route offers to the org's instance (empty ⇒ the configured
  default); `attestation.Service` resolves it per-org via the `issuerInstanceResolver`
  seam. `GET .../attestations/issuer-bundle` (and the Issuer tab) generate the full
  GitOps drop-in for an org's instance — `conf/issuer/<instance>.json`,
  `conf/dids/<instance>-did.json`, `conf/metadata/<instance>.json` (all the org's
  schema fragments + branding), and one `conf/vct/*.json` per schema — for an
  operator to commit to `openid4vc-poc-ops` and redeploy (Terraform wiring is
  manual per instance today; see that repo's `README-per-org-issuer.md`, incl. the
  credential-config-id uniqueness caveat). The seeded `yivi` org ships with instance
  `yivi` + en/nl schema translations and committed conf files.
- **Key material is a table + CRUD** but the actual signing instance is the global
  Veramo instance from config; per-org qualified certs are data-model-ready only.
- **Holder side (irmago + `held_attestations`) is NOT built** — deferred to a v2 plan
  (§6.5, open Qs 6–8). No Held tab; the KVK attestation does not yet surface here.
- Chained-attestation `linked_schema_ids` column exists; no wizard step (§7).
**Regulation:** COM(2025) 838 Art 5(1)(a), (f), (g), (h), Art 6(1)(a), (b), (k), (l),
Art 8; eIDAS (EU 910/2014) Art 3(16), 45a–45f (EAA), Art 5(1)(d)/(h) qualified
sign/seal. See `regulation/FEATURE_LIST.md`, `regulation/COM_2025_838_act.md`.
**Depends on:** `organization` (org-scoped authz, memberships, audit),
`identity`/`auth` (recipient matching, PID), `qerds` (offer/notification delivery
to a recipient's digital address), and a new **issuer provider seam** that mirrors
`openid4vpverifier`. Owner ID data holding is already partly realised by
`.ai/features/wallet-bootstrap.md` (the KVK registration attestation, Art 8).
**Sibling doc:** `.ai/features/auth-openid4vp.md` — that is the **verify/present**
half (OpenID4VP); this is the **issue** half (OpenID4VCI). Same requestor stance.
**Reference integration:** `/root/code/openid4vp-demo-frontend` `src/issuers.ts` —
a working **Veramo issuer** flow against the hosted issuer
`veramo-issuer.openid4vc.staging.yivi.app` (`create-offer` / `check-offer`,
pre-authorized-code grant). This is the concrete issuer we front, the mirror of the
hosted verifier the auth doc fronts.

---

## 1. Summary

An Electronic Attestation of Attributes (EAA) is a signed, verifiable credential
asserting facts about a subject. A Business Wallet must let its owner **issue,
request, obtain, select, combine, store, delete, share and present** EAAs
(Art 5(1)(a)), and specifically **issue EAAs to other Business Wallets and to EU
Digital Identity Wallets** (Art 5(1)(f)). This feature is the issuance side of that
obligation, plus the holder side (the org wallet holding and receiving EAAs).

Three directions, one domain:

1. **Issue** (org as issuer) — the org signs and delivers a credential to a
   recipient's wallet. Recipient is an **employee/member** (e.g. `nl.caesar.employee`,
   `nl.caesar.role`) **or any other party** — a customer, contractor, or another
   business (`nl.caesar.invoice`, `nl.caesar.contractor`, `nl.caesar.kyc`). This is
   the mockup's primary surface.
2. **Hold** (org as holder) — the org wallet stores the EAAs issued *to it*: its
   owner ID data (Art 8, already arriving via the KVK registration attestation), and
   EAAs other issuers grant it. Art 5(1)(a) "store, select, combine". The holder
   engine is **irmago's EUDI wallet** (`/root/code/irmago` `eudi/*`), embedded as a
   library and backed by **PostgreSQL** (§6.5) — a real EUDI holder (credential
   models, holder key binding, OpenID4VCI receive, OpenID4VP present), not a
   hand-rolled table.
3. **Receive** (inbound) — accept an offered EAA into the org wallet via irmago's
   OpenID4VCI **holder** flow (`eudi/openid4vci`), or over QERDS to the org's digital
   address (the wallet-bootstrap attestation is the first concrete case).

The mockup's three tabs map to the issue side: **Schemas** (credential type
definitions), **Templates** (named, reusable issuance presets over a schema, with an
issued count), **Issued** (the ledger of what has been issued, to whom, and its
status). "Held / received" is a fourth facet on the holder side (§6.5).

---

## 2. The stance: requestor/orchestrator, not the crypto or the QTSP

This is the same seam the codebase already takes twice — the hosted EUDI **verifier**
(`auth-openid4vp.md`) and the **QERDS** provider (`qerds.md`): *we orchestrate the
flow and own the domain data; we do not implement the protocol crypto or become the
trust service.*

Issuance is genuinely harder than verification on one axis: the issuer **holds a
signing key**. Verification only pins a trusted-issuer CA and decodes disclosures;
issuance must produce a signed SD-JWT VC. We keep the crypto out of our process:

- **We front the hosted Veramo OpenID4VCI issuer** the way we front the hosted
  verifier (`internal/openid4vciissuer`, §5). We call `create-offer` (as admin, with
  the attribute values we want in the credential) and poll `check-offer`; the Veramo
  issuer runs the OpenID4VCI token/credential handshake with the recipient's wallet
  and **signs the SD-JWT VC** with key material bound to the org's issuer instance. We
  never implement the token endpoint, proof-of-possession check, or SD-JWT signing —
  exactly as we never run the verifier crypto. Note the direction: unlike the verify
  side (where the wallet discloses claims to us), **we push the attribute values into
  the offer** (`credentialDataSupplierInput`); the issuer seals them.
- **Qualified vs non-qualified is a key-material distinction, not a code path**
  (§7). A **qualified** EAA (Art 5(1)(h)) must be sealed with a **qualified
  certificate** from a QTSP (Art 5(1)(d); `FEATURE_LIST.md` lists the NL QTSPs). A
  **non-qualified** EAA is signed with an org-scoped key the hosted issuer manages in
  a secure cryptographic device at assurance "substantial" (Art 6(1)(l)), attested by
  a **wallet unit attestation** (Art 6(1)(k)). Both flow through the same slice; the
  template picks which key material seals the credential. This is what the sidebar's
  **Key material** entry manages.

> We are the **issuer of record** (the org whose name is on the attestation) and the
> **orchestrator**; the QTSP/hosted issuer is the crypto. We never mint qualified
> seals ourselves, exactly as we never run the verifier or the QERDS QTSP.

---

## 3. Regulation mapping

| Design element | Article |
|---|---|
| Issue / request / obtain / select / combine / store / delete / share / present EAAs | Art 5(1)(a) |
| **Issue EAAs to Business Wallets and EU Digital Identity Wallets** | Art 5(1)(f) |
| Chained / linked attestations (an EAA linked to other relevant EAAs) | Art 5(1)(g) |
| Qualified **and** non-qualified EAAs; authorised-representative authentication | Art 5(1)(h) |
| Selective disclosure / data minimisation of issued attributes | Art 5(1)(b) |
| Common protocol for **issuance** of EAAs/certificates to wallets (OpenID4VCI) | Art 6(1)(a) |
| Relying parties request & validate EAAs (interop with the verify side) | Art 6(1)(b) |
| Wallet unit attestation (keys in a secure cryptographic device) | Art 6(1)(k) |
| Critical assets via secure crypto application/device at assurance "substantial" | Art 6(1)(l) |
| Qualified seal on qualified EAAs (needs a qualified certificate) | Art 5(1)(d) |
| Owner identification data as an attestation (official name + unique identifier) | Art 8 |
| Log of all transactions (issuance ledger) | Art 5(1)(m) |
| Export issued/held EAAs in a structured, machine-readable format | Art 5(1)(l) |
| Revocation of an issued attestation's validity | Art 6(2) (revocation posture) |

---

## 4. Architecture

New service-layer slice `internal/attestation` (orchestrates ≥2 stores + the issuer
client + key material + audit → follows the `auth`/`qerds` **with-service** template,
not the pure-CRUD `organization` template; `.ai/conventions/BACKEND.md` §Layering),
plus a provider seam `internal/openid4vciissuer` mirroring `openid4vpverifier`.

```
 admin/member (logged in, org-scoped)
        │  POST /api/v1/orgs/{slug}/attestations  {templateId, recipient, attributes}
        ▼
 ┌───────────────────────┐
 │  attestation.Service  │  orchestrates:
 └──┬───────┬────────┬───┘
    │        │        │
    │(1) resolve template+schema, validate attributes, resolve recipient
    │(2) persist issued_attestation(offered) + audit                 ─▶ issued_store
    │(3) create credential offer (key material from template) ───────▶ openid4vciissuer.Client ─▶ hosted issuer
    │        returns offerUri + issuance session id                        │
    │(4) recipient's wallet completes OpenID4VCI handshake ◀───────────────┘  (signs SD-JWT VC)
    │(5) poll issuance status → claimed | expired; advance state + audit
    │        (optional: deliver the offer link over QERDS to a digital address)
    ▼
 (holder side) inbound EAA ── qerds/webhook.go | irmago openid4vci receive ─▶ irmago EUDI holder engine
                                                                              (GORM/Postgres) + held_store index
```

Slice layout (house convention):

```
internal/attestation/
  handler.go        HTTP: parse, authz-in-context, status mapping, respond.*
  service.go        orchestration: issue flow, offer lifecycle, receive/hold, revoke
  schema_store.go   pgx CRUD for credential-type schemas
  template_store.go pgx CRUD for issuance templates
  issued_store.go   pgx CRUD for the issued-attestation ledger
  held_store.go     pgx CRUD for EAAs the org holds/received
  status.go         issuance + revocation state-machine constants
  attestation.go    domain types + package sentinel errors
```

`internal/openid4vciissuer/` — the external client (the `openid4vpverifier`
analogue): `CreateOffer`, `Result`, `Ping`, plus a `RequestAuthenticator` seam and a
local stub. See §5.

Routes register via the `Registerer` interface. Org-scoped routes compose
`auth.RequireUser` → `organization.Handler.Authorize` (member) → optional
`organization.RequireOrgAdmin` (schema/template/key management, issuance). Identity
stays central and slug-free. The recipient's wallet-facing OpenID4VCI callbacks hit
the hosted issuer, **not** our session-cookie chain (machine-to-machine, Art 6(1)(d)).

---

## 5. The issuer client seam — `internal/openid4vciissuer`

Mirrors `openid4vpverifier` one-for-one (thin typed client, config-swappable
authenticator, fatal boot `Ping`, correlate-by-provider-token, local stub). The
concrete driver targets the **Veramo issuer** API (demo `src/issuers.ts`), which is
addressed **per issuer instance** (`{ISSUER_BASE}/{issuerName}/api/...`) and
authenticated with a **Bearer admin token**:

- `CreateOffer(ctx, OfferRequest) (Offer, error)` →
  `POST {ISSUER_BASE}/{issuerName}/api/create-offer` with
  ```jsonc
  { "credentials": ["<credentialId>"],
    "grants": { "urn:ietf:params:oauth:grant-type:pre-authorized_code":
                { "pre-authorized_code": "generate" } },   // optional tx_code: numeric,6
    "credentialMetadata": { "expiration": <seconds> },
    "credentialDataSupplierInput": { <attribute values> } }   // the claims WE supply
  ```
  Returns `{ id, uri, txCode? }` → we expose `{ issuanceId: id, offerUri: uri, txCode }`;
  `offerUri` is the `openid-credential-offer://...` deeplink rendered as a QR +
  universal link (same `walletLink`/`QrCodeComponent` family as login). The demo's
  pre-auth `"pre-authorized_code": "generate"` has the issuer mint the code; use
  **`tx_code`** (a 6-digit out-of-band PIN the recipient types into their wallet) when
  the offer link travels over an untrusted channel (email/QERDS) to bind the claim to
  the intended recipient — the issuance analogue of the verifier's random nonce.
- `Result(ctx, issuanceId) (Issuance, error)` →
  `POST {ISSUER_BASE}/{issuerName}/api/check-offer {"id": issuanceId}`; poll until
  `status == "CREDENTIAL_ISSUED"` (→ `claimed`), else pending/expired. Poll
  server-side; the client never sees the issuer's internal transaction id (reuse the
  `presentation`-style opaque-id indirection).
- `Ping(ctx)` boot probe → "can we create an offer of the shape we'll use?" Fatal on
  failure (a misconfigured issuer fails the deploy, not the first issuance — same as
  the verifier probe). `/readyz` stays DB-only.

`OfferRequest` carries the **issuer instance** (`issuerName`) and **`credentialId`**
resolved from the template's key material + schema (§7): the schema's `vct` maps to a
Veramo `credentialId` registered on that issuer instance, and the instance holds the
signing key. We never hold the private key in-process. The demo also supports an
`authorization_code` grant (`issuer_state: "generate"`); v1 defaults to pre-authorized.

**Local `StubIssuer`** (dev/CI, `ATTESTATION_ISSUER=stub`): deterministic, returns a
fake `offerUri` and, after a short delay, flips `check-offer` to `CREDENTIAL_ISSUED` so
the whole offer→claim loop runs offline — the issuer equivalent of the QERDS
`StubProvider` and the faked verifier. Selected by config, never code.

---

## 6. Data model (Postgres, goose migrations, gen_random_uuid PKs)

One logical table per migration (convention); `TIMESTAMPTZ DEFAULT now()`, always a
`down`, edit-in-place pre-prod; `camelCase` JSON at the edge.

### 6.1 `attestation_schemas` — the credential-type definition (Schemas tab)

The "what fields exist" for a credential type. Org-scoped; the `vct` is globally
namespaced under the org (`nl.caesar.employee`).

| column | type | notes |
|---|---|---|
| `id` | uuid PK | `gen_random_uuid()` |
| `org_id` | uuid FK→organizations | `ON DELETE CASCADE` |
| `vct` | text | credential type, e.g. `nl.caesar.employee`; unique per org |
| `display_name` | text | e.g. "Employee of Caesar Groep" |
| `attributes` | jsonb | ordered list `[{key, label, type, required, display?}]` — `type` is one of `SupportedAttributeTypes` (`string`/`integer`/`number`/`boolean`/`date`); `display` is the optional per-language SD-JWT VC claim labels `[{lang, label}]` |
| `display` | jsonb | credential-level SD-JWT VC type metadata `display` array `[{lang, name}]` — per-language display names for wallets |
| `qualified` | boolean | whether this type is issued as a **qualified** EAA (Art 5(1)(h)) |
| `status` | text CHECK | `draft` \| `active` \| `deprecated` |
| `created_at` / `updated_at` | timestamptz | |

### 6.2 `attestation_templates` — a named issuance preset (Templates tab)

The "what we issue and how it's labelled", bound to a schema. Carries defaults,
validity, branding, and the key-material choice. The card's "N issued" is a count of
`issued_attestations` for this template.

| column | type | notes |
|---|---|---|
| `id` | uuid PK | |
| `org_id` | uuid FK→organizations | `ON DELETE CASCADE` |
| `schema_id` | uuid FK→attestation_schemas | the type this template issues |
| `name` | text | e.g. "Board signing authority" |
| `default_attributes` | jsonb NULL | pre-filled values for the wizard |
| `validity_period` | interval NULL | issued-credential lifetime (→ `expires_at`) |
| `key_material_id` | uuid FK→attestation_keys NULL | which key/cert seals it (§7); null ⇒ org default |
| `linked_schema_ids` | uuid[] NULL | chained attestations (Art 5(1)(g)) — types this one links to |
| `status` | text CHECK | `active` \| `archived` |
| `created_at` / `updated_at` | timestamptz | |

### 6.3 `issued_attestations` — the issuance ledger (Issued tab)

One row per issuance attempt; the transaction log the regulation requires
(Art 5(1)(m)). Never hard-deleted (revoke is a state change).

| column | type | notes |
|---|---|---|
| `id` | uuid PK | |
| `org_id` | uuid FK→organizations | |
| `template_id` | uuid FK→attestation_templates | `ON DELETE SET NULL` (keep the ledger row) |
| `schema_vct` | text | denormalised type at issue-time (schema may later change) |
| `recipient_kind` | text CHECK | `member` \| `external` |
| `recipient_user_id` | uuid FK→users NULL | set when `member` |
| `recipient_ref` | text | email / digital address / name shown in the UI |
| `attributes` | jsonb | the exact attribute values issued (source of the "attributes" step) |
| `qualified` | boolean | snapshot of whether a qualified seal was used |
| `status` | text CHECK | `offered` \| `claimed` \| `expired` \| `revoked` \| `failed` |
| `issuance_id` | text | opaque id into `openid4vciissuer` (correlation key) |
| `linked_attestation_id` | uuid FK→issued_attestations NULL | chained/linked (Art 5(1)(g)) |
| `qualified_timestamp` | timestamptz NULL | when a QTST anchors the seal (if qualified) |
| `issued_by_user_id` | uuid FK→users NULL | the admin/member who issued it |
| `claimed_at` / `expires_at` / `revoked_at` | timestamptz NULL | lifecycle stamps |
| `created_at` / `updated_at` | timestamptz | |

### 6.4 `attestation_keys` — key material (sidebar "Key material")

The seam to the signing material. We **do not store private keys** — this row
references key material held in the hosted issuer / secure crypto device (Art 6(1)(l))
or a QTSP-issued qualified certificate.

| column | type | notes |
|---|---|---|
| `id` | uuid PK | |
| `org_id` | uuid FK→organizations | |
| `kind` | text CHECK | `wallet_managed` (non-qualified) \| `qualified_certificate` (QTSP) |
| `label` | text | human name in the UI |
| `provider_ref` | text | reference into the hosted issuer / QTSP key store — **never the key** |
| `certificate_pem` | text NULL | public cert chain for a qualified cert (never the key) |
| `wallet_unit_attestation` | jsonb NULL | Art 6(1)(k) attestation of the secure device |
| `status` | text CHECK | `active` \| `suspended` \| `revoked` |
| `valid_from` / `valid_until` | timestamptz NULL | |
| `created_at` / `updated_at` | timestamptz | |

### 6.5 Held credentials — irmago EUDI holder engine, PostgreSQL-backed

Art 5(1)(a) "store, select, combine". Rather than hand-roll a credential store, embed
**irmago's EUDI wallet** (`/root/code/irmago` `eudi/storage`) as the holder engine.
Its storage is **GORM** behind a `Storage` interface exposing `Db() *gorm.DB` and an
`AutoMigrate` over credential-holder models (`CredentialMetadata`, `CredentialClaim`,
`CredentialDisplay`, `IssuedCredentialInstance`, `HolderBindingKey`,
`ECDSAKeyMetadata`, `CredentialBatch`, `EudiLogEntry`, …). The default dialector is
**sqlcipher (encrypted SQLite)** with a per-wallet AES key.

**The swap the user asked for = a GORM dialector swap**, sqlcipher → `gorm.io/driver/
postgres`, keeping the same models + `AutoMigrate`. irmago's `NewStorage` hardcodes
the sqlcipher connector today, so this needs a small seam in irmago (accept a
`gorm.Dialector`, or a `NewStorageWithDialector`) — the models are dialector-agnostic
GORM structs, so no model changes. Two consequences that **must** be designed for:

- **At-rest encryption.** sqlcipher gives encryption at rest with a per-wallet AES key
  for free; plain Postgres does not. Credentials are sensitive (owner ID data, KYC).
  Keep the property: column-level encryption (pgcrypto / app-level envelope encryption
  with a per-org key), or an explicitly accepted DB/volume-encryption posture. Do not
  silently drop it in the dialector swap.
- **Multi-tenancy.** irmago assumes one wallet = one DB + one AES key; our deployment
  is one Postgres for many orgs. Isolate per org — `org_id` scoping + per-org
  encryption key, aligned with the per-org RLS direction in the root `CLAUDE.md`
  multi-tenancy note. The irmago holder engine is instantiated per org (its own key),
  behind our slice.

Our slice keeps a **thin index** (`held_attestations`) over irmago's credential rows
for org-scoping, listing, audit, and the QERDS evidence link — it points at the
irmago-owned credential, it does not duplicate the claims:

| column | type | notes |
|---|---|---|
| `id` | uuid PK | |
| `org_id` | uuid FK→organizations | tenancy scope |
| `credential_ref` | text | id of the irmago `IssuedCredentialInstance` this indexes |
| `vct` | text | credential type held (denormalised for listing/filter) |
| `issuer` | text | who issued it (KVK, another business, a QTSP) |
| `source` | text CHECK | `qerds` \| `openid4vci` \| `bootstrap` |
| `source_message_id` | uuid FK→qerds_messages NULL | evidence chain when it arrived over QERDS |
| `received_at` / `deleted_at` | timestamptz NULL | soft-delete keeps the trail |
| `created_at` / `updated_at` | timestamptz | |

> **Note on re-introducing irmago.** `auth-openid4vp.md` deliberately *removed* irmago
> from the **verify/login** path (protocol-swapped to the hosted EUDI verifier). This
> re-adds it for a **different role** — the **holder** wallet engine — not the verifier
> daemon. That is not a reversal of that decision, but it does re-add the `irmago`
> dependency to `backend/`; call it out in the plan (§15).
>
> **Implemented (Phase 2), `internal/eudiholder`.** The `Holder` provider seam mirrors
> the `openid4vciissuer` stub/veramo pattern: `ATTESTATION_HOLDER=stub` (in-memory,
> default) or `irmago` (config-selected, never code). The irmago `Engine` opens one
> `storage.NewStorageWithDialector(postgres.Open(dsn?search_path=holder_<orghex>), fs)`
> **per org**, lazily + cached, with a fatal boot `Ping`. **Isolation is per-org Postgres
> schema, not `org_id` rows** — irmago's holder models carry no tenant column, so
> row-scoping would fork irmago; each org's tables live in `holder_<orghex>` on the shared
> DB. **At-rest encryption:** the raw credential (`RawCredential`) rests under the
> DB/volume posture — sqlcipher's per-field encryption is unavailable on Postgres and
> irmago owns the write path, so per-org app-level envelope encryption is deferred (needs
> an irmago seam), documented not dropped; a per-org filesystem key derived from
> `ATTESTATION_HOLDER_MASTER_KEY` protects irmago's on-disk trust material. Consuming
> irmago required an upstream split (privacybydesign/irmago#622) so `eudi/storage` imports
> **without** the cgo `sqlcipher` package (the `CGO_ENABLED=0` Alpine build + `go test
> -race` both forbid it).

---

## 7. Qualified vs non-qualified, and chained attestations

- **Non-qualified EAA** — `template.key_material_id` → a `wallet_managed` key; the
  hosted issuer signs with an org key in a secure crypto device (Art 6(1)(l),
  assurance "substantial"), attested by a wallet unit attestation (Art 6(1)(k)). No
  QTSP needed. This is the default for internal HR-style credentials
  (`nl.caesar.employee`, `nl.caesar.role`).
- **Qualified EAA** — `template.key_material_id` → a `qualified_certificate` key
  bound to a QTSP-issued qualified certificate (Art 5(1)(d)/(h); NL QTSPs in
  `FEATURE_LIST.md`). Optionally anchored with a **qualified timestamp** (Art 5(1)(e),
  a QTSA) → `issued_attestations.qualified_timestamp`. Used where legal weight matters
  (`nl.caesar.board` signing authority, KYC).
- **Chained / linked attestations (Art 5(1)(g))** — a template may declare
  `linked_schema_ids`, and an issued attestation may carry `linked_attestation_id`, so
  a credential references other relevant EAAs (e.g. a role credential linked to the
  employment credential). The link is recorded in our ledger and expressed in the
  OpenID4VCI offer; the hosted issuer embeds it in the VC.

The distinction is entirely data (which key seals it) — a single issuance code path.

---

## 8. State machine (`status.go`)

```
issued_attestations:
  (issue) ─────────────────────▶ offered      (credential offer created)
  offered ──(wallet claims)────▶ claimed       (OpenID4VCI handshake done, VC delivered)
  offered ──(TTL elapses)──────▶ expired
  offered ──(issuer error)─────▶ failed        (retryable — offer can be re-created)
  claimed ──(owner/holder)─────▶ revoked        (Art 6(2) — validity revoked)

attestation_keys:  active ──▶ suspended | revoked   (compromise, QTSP drop-off)
held_attestations: received ──▶ deleted (soft)      (owner delete, Art 5(1)(a))
```

Poll transitions are idempotent (dedupe on `issuance_id`), same discipline as the
QERDS webhook and the verifier poll.

---

## 9. Flows

### 9.1 Define a schema (Schemas tab, admin)
`POST /api/v1/orgs/{slug}/attestations/schemas {vct, displayName, attributes[], qualified}`
→ validates the `vct` namespace + attribute list, persists `draft`/`active`, audits
in-tx. Editing a schema in use is additive/versioned (a live template keeps its
denormalised `schema_vct`).

### 9.2 Create a template (Templates tab, admin — "New template")
`POST .../attestations/templates {schemaId, name, defaultAttributes?, validityPeriod?, keyMaterialId?, linkedSchemaIds?}`
→ binds to a schema, persists `active`, audits. The card's attribute chips come from
the schema; "N issued" is a count over `issued_attestations`.

### 9.3 Issue an attestation (the wizard — "Issue attestation")
Matches the mockup's 3 steps (progress bar):
1. **Template** — pick a template (pre-selects its schema + key material).
2. **Recipient** — an **employee/member** (picked from `memberships`, pre-fills known
   attributes like the mockup's "Anna de Vries / Platform / Engineering Lead") **or an
   external party** (email / digital address).
3. **Attributes** — edit the pre-filled attribute fields; then **review**.
On submit: `POST .../attestations {templateId, recipient, attributes}` →
`attestation.Service` validates attributes against the schema, persists
`issued_attestations(offered)` + audit **in one tx**, then calls
`openid4vciissuer.CreateOffer` with the template's key material. Returns
`202 {id, status, offerUri}`. The UI renders `offerUri` as a QR + "open wallet" link
(same component family as the login QR); for a **remote** recipient the offer link is
delivered over **QERDS** to their digital address (reusing the `qerds` send path) or
by email. The service polls `Result`; on claim → `claimed` + `claimed_at` + audit; on
TTL → `expired`.

### 9.4 Revoke an issued attestation (admin, Art 6(2))
`POST .../attestations/{id}/revoke` → `status → revoked`, `revoked_at`, propagate
revocation to the issuer's status list (StatusList2021-style; provider seam call),
audit. Revocation propagation to external validators beyond the status list is v2.

### 9.5 Receive / hold (holder side, Art 5(1)(a))
Inbound EAAs land in the org's **irmago EUDI holder engine** (§6.5) with a
`held_attestations` index row: the KVK registration attestation via `qerds/webhook.go`
(`source=bootstrap`, already produced by wallet-bootstrap), an EAA offered to the org
over OpenID4VCI accepted through irmago's holder flow (`source=openid4vci`), or one
delivered over QERDS (`source=qerds`, with the `qerds_evidence` chain). The org can
list, select/combine, and delete held EAAs (soft-delete). Export (§10, Art 5(1)(l))
covers both issued and held.

The `source=qerds` path — delivering a real OpenID4VCI Credential Offer over the
secure channel (not a claim link) and redeeming it via the holder's OpenID4VCI
flow — is designed in [`oid4vci-over-qerds.md`](./oid4vci-over-qerds.md) (pre-auth
vs. authorization-code grants for business wallets).

**Display** of held credentials uses irmago's own read/display model: the held
list endpoint returns `HeldCredentialView` = the index row merged with
`clientmodels.Credential` (the DTOs the irmamobile wallet renders — localized
name, issuer, attributes, logos), read from the org's storage via irmago's
`CredentialService.GetCredentialMetadataList`. This is the first step of moving
issue/present/hold onto irmago-native abstractions as **config-selectable
providers** (hosted issuer/verifier stay the default); native OpenID4VP
presentation and native issuance are planned follow-ups.

---

## 10. HTTP API

Return `*respond.APIError` for client errors; wrap handlers in `respond.HandlerFunc`.
All org-scoped (`auth.RequireUser` → `organization.Handler.Authorize`, org via
`OrgFromContext`). Write/manage routes add `RequireOrgAdmin`.

**Schemas** (admin): `GET|POST .../attestations/schemas`, `GET|PATCH|DELETE .../attestations/schemas/{id}`
**Templates** (admin): `GET|POST .../attestations/templates`, `GET|PATCH|DELETE .../attestations/templates/{id}`
**Issued ledger** (member read; admin issue/revoke):
- `GET  .../attestations` — the Issued tab (filter by template/status/recipient)
- `POST .../attestations` — issue (§9.3) → `202 {id, status, offerUri}`
- `GET  .../attestations/{id}` — poll one issuance `{status, claimedAt?, offerUri?}`
- `POST .../attestations/{id}/revoke` — admin
**Key material** (admin): `GET|POST .../attestations/keys`, `POST .../attestations/keys/{id}/suspend|revoke`
**Held** (member read; admin delete): `GET .../attestations/held`, `DELETE .../attestations/held/{id}`
**Export** (Art 5(1)(l)): `GET .../attestations/export` — structured, machine-readable
(issued + held), admin.

The recipient wallet's OpenID4VCI callbacks hit the **hosted issuer**, not our routes.

---

## 11. Frontend

3-part pattern (`src/api/attestations.ts` client + `attestations.queries.ts` hooks +
`src/routes/attestations/*`); keys nested under the org detail key so org
invalidation cascades. The screen is the mockup:
- **Tabs**: Templates (cards: display name, `vct`, attribute chips, "N issued",
  Issue →), Issued (ledger table: recipient, template, status, issued-at), Schemas
  (type definitions + attribute editor).
- **"Issue attestation" wizard** (modal, 3 steps + progress bar): template →
  recipient (member picker with attribute pre-fill, or external email/address) →
  attributes → review → submit; on success render the `offerUri` QR + "open wallet"
  link and poll to `claimed`.
- **"New template"** and schema editors (admin-gated).
- **Key material** view (sidebar) for qualified vs wallet-managed keys.
- i18n under `attestations.*`; inline errors via the `suppressErrorToast` convention.

---

## 12. Trust, audit, export

- **Audit** (`internal/audit`): new actions `SchemaCreated/Updated`,
  `TemplateCreated/Updated/Archived`, `AttestationIssued`, `AttestationClaimed`,
  `AttestationRevoked`, `KeyMaterialAdded/Suspended/Revoked`, `HeldAttestationDeleted`;
  new targets `TargetAttestationSchema`, `TargetAttestationTemplate`,
  `TargetIssuedAttestation`, `TargetAttestationKey`, `TargetHeldAttestation`. Standard
  `{before, after}` envelope; store readable values, never ids. Every mutation in-tx.
- **Data minimisation** (Art 5(1)(b)): only the attributes the template declares are
  issued; the schema is the allow-list. The verify side (`auth-openid4vp.md`) already
  enforces selective disclosure on presentation.
- **Export** (Art 5(1)(l)): a structured machine-readable dump of issued + held EAAs
  and their ledger/evidence, for portability and service termination.

---

## 13. Config (env-driven, `internal/config`)

| Var | Meaning | Required |
|---|---|---|
| `ATTESTATION_ISSUER` | `stub` (dev) or `veramo` | no (default `stub`) |
| `ATTESTATION_ISSUER_URL` | Veramo issuer base (`veramo-issuer.openid4vc.staging.yivi.app`) | when `veramo` |
| `ATTESTATION_ISSUER_ADMIN_TOKEN` | Veramo issuer Bearer admin token | when `veramo` |
| `ATTESTATION_ISSUER_INSTANCE` | Veramo issuer instance name (`issuerName` in the path) | when `veramo` |
| `ATTESTATION_OFFER_TTL` | credential-offer lifetime before `expired` | no (has default) |
| `ATTESTATION_VCT_NAMESPACE` | org `vct` namespace prefix (e.g. `nl.<org>`) | no (derived) |
| `ATTESTATION_HOLDER_ENCRYPTION_KEY` | per-org holder at-rest key material (§6.5) | when holder enabled |

Boot `Ping` is **fatal** (matches the verifier/QERDS readiness gate). Qualified-EAA
signing additionally needs a QTSP-issued qualified certificate referenced by an
`attestation_keys` row — provisioned per org, not global env. The holder engine reuses
the existing `DATABASE_URL` (Postgres) via the irmago dialector swap (§6.5).

---

## 14. Scope

**In (v1 design):** the `attestation` slice (schemas, templates, issued ledger, held);
the `openid4vciissuer` provider seam + stub; issue → offer → claim over OpenID4VCI
(pre-authorized code); member and external recipients; QR/deeplink render + poll;
non-qualified (wallet-managed key) issuance; the Schemas/Templates/Issued tabs + issue
wizard; audit + export; **holder side via the embedded irmago EUDI engine backed by
Postgres** (GORM dialector swap + per-org isolation + at-rest encryption), with the
KVK attestation from bootstrap surfacing as the first held credential.

**Out (v1):** a real hosted issuer / QTSP integration (stub only); qualified-seal
issuance against a real qualified certificate + QTST (data model ready, key
provisioning is a QTSP onboarding step); status-list revocation propagation to
external validators beyond flipping our own list; ISO-mdoc credential format
(SD-JWT VC only, same as the verify side); the European Digital Directory for
resolving external recipients' digital addresses (reuse the `qerds` `ResolveAddress`
seam + interim contacts).

---

## 15. Open questions

1. **Issuer instance provisioning** — the hosted issuer exists (Veramo,
   `veramo-issuer.openid4vc.staging.yivi.app`, demo `src/issuers.ts`). Open: is there
   **one Veramo issuer instance per org** (clean issuer-of-record + key isolation) or a
   shared instance with per-offer `credentialId`s? And how is an org's `credentialId`
   set registered on the instance from our schema definitions? Gates §5/§6.4.
2. **Qualified key provisioning + WUA-backed activation** — tracked in **#28**
   (qualified trust services). Which NL QTSP issues the org's qualified certificate, how
   is `attestation_keys.provider_ref` bound to it, and where does the **wallet unit
   attestation** (Art 6(1)(k)/(l)) come from? The WUA is also the missing piece of
   *wallet activation*: today wallet-bootstrap's `ActivateFromAttestation` only flips the
   business-wallet record to `active`, whereas assurance-backed activation would provision
   a WUA proving the wallet runs on a secure cryptographic device at assurance
   "substantial". This stays a **server-side / remote-QSCD orchestrator** model (we never
   hold private keys; no device-resident PIN/holder-binding ceremony — see
   `wallet-bootstrap.md`). Likely shaped as a new provider seam (WUA/QSCD source) mirroring
   the issuer/holder/qerds seams. (Ties to `FEATURE_LIST.md` QTSP list.)
3. **External recipient reach** — for a non-member recipient with no known digital
   address, is delivery email-link only until the European Digital Directory lands, or
   QERDS-first? (Reuses the `qerds` resolution seam.)
4. **Schema governance** — are org `vct` types free-form, or must they be registered
   in a shared type catalogue for cross-org verifiability? Affects §6.1 uniqueness and
   the verify-side trust list.
5. **Revocation model** — StatusList2021 vs a QERDS-delivered revocation notice; how a
   relying party checks a revoked EAA. (Verify-side dependency.)
6. ~~**irmago Postgres seam**~~ **RESOLVED (Phase 2).** Pushed upstream:
   `NewStorageWithDialector` (irmago#620) + a sqlcipher-package split (irmago#622) so
   `eudi/storage` is CGO-free. Re-adding the `irmago` dependency to `backend/` for the
   **holder** role was signed off (distinct from the removed verifier daemon).
7. ~~**Holder at-rest encryption + multi-tenancy**~~ **RESOLVED (Phase 2).** Multi-tenancy:
   **per-org Postgres schema** (`holder_<orghex>`), not `org_id`-scoped rows (irmago's
   models have no tenant column). At-rest: **DB/volume-level posture** documented;
   app-level envelope encryption of `RawCredential` deferred (needs an irmago write-path
   seam). No `ATTESTATION_HOLDER_ENCRYPTION_KEY` yet.
8. ~~**One holder engine or per-org instances**~~ **RESOLVED (Phase 2).** Per-org engine
   instance (one `storage.Storage` per org, lazily opened + cached, bounded pool), its own
   filesystem key. The perf/connection trade-off is bounded by `engineMaxOpenConns`.

---

## 16. Done when (if built)

- An admin defines a schema + template, issues an attestation to a member and to an
  external party; both produce an `offered` ledger row and a rendered `offerUri`; the
  stub issuer flips them to `claimed` end-to-end (integration test against
  `TEST_DATABASE_URL`).
- Every attestation mutation (schema/template/issue/claim/revoke/key/held) is audited
  in-tx; the Issued tab reflects status transitions.
- Data minimisation holds: issuing an attribute not declared by the schema is rejected.
- Non-qualified issuance works with a `wallet_managed` key; the qualified path is
  reachable by swapping `key_material_id` to a `qualified_certificate` row (crypto
  stubbed).
- The irmago EUDI holder engine runs against Postgres (dialector swap), scoped per
  org and encrypted at rest; the KVK registration attestation (from
  `wallet-bootstrap.md`) surfaces as a held credential; export returns issued + held
  in a structured, machine-readable form.
- Boot `Ping` fails fast when the issuer can't accept an offer of our shape.
- Backend + frontend verify sequences (`CLAUDE.md`) pass.

## Harvest

- Convention to add/update: note the **issuer** provider-seam pattern in
  `.ai/conventions/BACKEND.md` alongside the existing verifier/QERDS seam note on
  merge (`openid4vciissuer` mirrors `openid4vpverifier`).
- Feature docs: **this file**; cross-link from `.ai/features/auth-openid4vp.md`
  (verify↔issue siblings) and `.ai/features/wallet-bootstrap.md` (the KVK attestation
  is the first `held_attestations` producer). Promote from Design to built on merge.
