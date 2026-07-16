# Feature: Business Wallet Bootstrap (KVK attestation over QERDS)

**Status:** Implemented end-to-end (demo). A logged-in user enrolls at `/enroll`
by entering a KVK number; the backend **synchronously consults the registry**
(`registryprovider.Consult`, mocked) and, if the caller is a listed
representative, atomically creates the org + address + owner membership +
representations and activates the wallet (`ActivateFromAttestation`,
integration-tested). Frontend enrollment screen + org-switcher entry.

Mock: `WALLET_REGISTRY_PROVIDER=stub` returns **Yivi B.V. (KVK 94861412)** with
Dibran Mulder as a **gevolmachtigde / beperkt** (beperkt volmacht); any other
number returns a generic company so the flow always demoes.

Divergences from the async design below (deliberate, for this iteration):
- **Consult is synchronous**, not a two-legged QERDS request+attestation. The
  faithful async path (outbound over QERDS, inbound webhook →
  `HandleAttestation`) remains the target; `HandleAttestation` is reused by the
  sync path.
- **No PID disclosure at enrollment** — the mock keys on the KVK number, and
  login is email-only, so the requester's verified identity is not sent to KVK
  yet. A real integration would present the PID (see `auth-openid4vp.md`
  `ScopeIdentity`).
- The requester is granted **admin** regardless of representation kind (they
  bootstrap the wallet). Role-by-kind (bestuurder→admin, gevolmachtigde→scoped)
  is a refinement.

Entry points: a **public `/register`** page (no account needed — the registrant
authenticates via an OpenID4VP identity disclosure, the account is created if new,
and they're logged in on success), and an authenticated `/enroll` for a logged-in
user. Both go through `OpenWallet`. On activation the KVK attestation is also
**deposited into the new org's QERDS inbox** as an inbound message with delivery
evidence (`registratie@kvk.nl`). One wallet per company: re-registering an
already-active KVK returns `409 already_registered` (a second representative should
join via a claim, not a duplicate).

Still pending: owner-claim matching (`ClaimRepresentation`, 501); a shared strong
identifier for matching; the async QERDS request→attestation transport.
**Depends on:** the `qerds` slice (messages/addresses/evidence/provider); **OpenID4VP/EUDI
login** (`.ai/features/auth-openid4vp.md`) for the PID/identity seam; `organization`
(orgs/memberships/audit); `identity` (name reconciliation).
**Regulation:** COM(2025) 838 — EBW. See `regulation/FEATURE_LIST.md`.

---

## 1. Summary

An economic operator bootstraps a **business wallet instance** by proving, over a
**qualified electronic registered delivery service (QERDS)**, that it exists in an
authentic register (KVK, the Dutch business register) and who is authorised to act for it.

The flow, as agreed:

1. A person **opens a new business wallet** → the wallet instance is created and
   **generates its own QERDS digital address** (Art 6(1)(j)) so it can receive KVK's reply.
   It starts in a `provisioning` state.
2. The person's **PID** — their verified identity credential (passport or id-card,
   presented via OpenID4VP at login; see `.ai/features/auth-openid4vp.md`) — is sent to KVK
   **over QERDS** together with the **KVK number**.
3. **KVK is the one that authorizes.** As the authentic source, KVK matches the PID against
   its register and returns a **registration attestation over QERDS**, carrying the org
   context — **legal name, EUID, KVK number** — and the **representative list**
   (bestuurders, gevolmachtigden), marking the requesting person as a listed representative.
4. On receipt (with evidence), the wallet **activates**: the org identity is populated, the
   representatives are recorded, and the requester becomes the **first owner**. The other
   representatives become **claimable owners** (§6.3) — no separate KVK round-trip.

### Why QERDS is the spine, and why KVK does the matching

The wallet's owners are not established because someone typed names into a form. They are
established because a **registered delivery from KVK** — proof of origin, integrity,
qualified timestamp, non-repudiation — says so. The QERDS **evidence** (append-only
`qerds_evidence`) is what gives the ownership graph legal standing under Art 4 equivalence.

Critically, **the authorization check lives at KVK, not in our code.** We send the PID +
KVK number; KVK matches the natural person against the register and asserts the result. We
never guess who someone is by fuzzy-matching a name — the authentic source asserts it, with
evidence. This is the core anti-fraud property (cf. the invoice-fraud surface the
regulation targets, where directorship is asserted by email/portal with "limited
guarantees of authenticity").

### Regulation mapping

| Design element | Article |
|---|---|
| Secure remote onboarding via an authorised representative (assurance substantial/high) | Art 6(1)(e) |
| Owner identification data = official name + unique identifier, issued as attestation | Art 8 |
| Reuse the **EUID** unique identifier (BRIS/BORIS national registers) | Art 9 |
| Transmit/receive with confidentiality + integrity (QERDS) | Art 5(1)(i) |
| One unique digital address per owner | Art 6(1)(j) |
| Multi-user authorisation / mandate & representation management | Art 5(1)(j) |
| Representation **verifiable, auditable, revocable, traceable**; over-delegation & expiry detected | Art 6(2) |
| Wallet validity **revocation** (owner request, compromise, cessation) | Art 6(2) |

> KVK is the NL realisation of Art 9's "national registers." We are the **integrator /
> requestor**, never the register and never the QTSP — the stance the codebase already
> takes for the EUDI verifier (`.ai/features/auth-openid4vp.md`) and QERDS
> (`.ai/features/qerds.md`).

---

## 2. Concepts & terminology

- **KVK number** — the national register number (NL).
- **EUID** — European Unique Identifier (Art 9), NL-derived from the KVK number via BRIS.
- **PID** — the person's verified identity credential (passport or id-card SD-JWT VC),
  obtained via OpenID4VP at login. This is what we present to KVK.
- **Bestuurder** — director/board member → owner-grade authority.
- **Gevolmachtigde (volmacht)** — proxy / power of attorney → scoped authority.
- **Representation** — the *legal claim*, sourced from the KVK attestation, that a named
  person may act for the org. Distinct from **membership**.
- **Membership** — realised *application access* (`organization.Membership`). A
  representation is *claimed* → becomes a membership. Keeping them separate is what lets
  representation stay "verifiable/auditable/revocable/traceable" (Art 6(2)) independently of
  who has actually logged in.

---

## 3. Architecture

New service-layer slice `internal/wallet` (orchestrates ≥2 stores/clients → follows the
`auth` template, not the pure-CRUD `organization` template — `.ai/conventions/BACKEND.md`
§Layering).

```
 person (logged in via OpenID4VP: passport|idcard + email + phone)
        │  POST /api/v1/wallet  {kvkNumber}          (central, slug-free)
        ▼
 ┌────────────────────┐
 │   wallet.Service   │  orchestrates:
 └──┬───────┬─────┬───┘
    │        │     │
    │(1) create wallet_instance(provisioning) + provision QERDS address  ─▶ qerds address_store
    │
    │(2) send {PID, kvkNumber} to KVK  ─────────────────────────────────▶ qerds.Service (outbound, over QERDS)
    │                                                                          │
    │(3) KVK attestation arrives  ◀────── qerds/webhook.go (inbound + evidence)┘
    │        content-type nl.kvk.registration-attestation.v1
    │
    │(4) on attestation: activate (single tx) ─▶ organization.Store (Create org, AddMembership)
    │        populate identity, insert representations, first owner, mark active  + audit
    ▼
 registryprovider.Client  (KVK request driver: stub | bris)  — dev stub simulates KVK's inbound reply
```

Reuse (no new transport): the QERDS message path + `qerds/webhook.go` inbound receiver
(the attestation is a QERDS message of content type `nl.kvk.registration-attestation.v1`),
append-only `qerds_evidence`, `qerds_addresses`/`provisionAddress`, the PID from
OpenID4VP login, `identity.Reconcile`, and the accept-via-presentation shape from
`organization/accept.go`.

---

## 4. Data model (new migrations)

`.ai/conventions/BACKEND.md` §Migrations: one table per file, `uuidv7()` PKs,
`TIMESTAMPTZ DEFAULT now()`, always a `down`, edit-in-place pre-prod.

### 4.1 `wallet_instances` — the lifecycle entity (created at "open wallet")

The wallet instance is created **first** (provisioning-first), before the org identity is
known — the org row is populated only when KVK's attestation confirms it.

| column | type | notes |
|---|---|---|
| `id` | uuid PK | `uuidv7()` |
| `status` | text CHECK | `provisioning` \| `awaiting_attestation` \| `active` \| `rejected` \| `suspended` \| `revoked` |
| `requestor_user_id` | uuid FK→users | the person who opened it; `ON DELETE SET NULL` |
| `kvk_number` | text | the number the requester entered |
| `digital_address` | text | provisioned at open-time (Art 6(1)(j)) |
| `organization_id` | uuid FK→organizations NULL | **null until `active`** (unique when set) |
| `legal_name` | text NULL | populated from the attestation |
| `euid` | text NULL | populated from the attestation |
| `request_message_id` | uuid FK→qerds_messages NULL | outbound {PID, KVK} to KVK |
| `attestation_message_id` | uuid FK→qerds_messages NULL | inbound KVK attestation |
| `reject_reason` | text NULL | e.g. `not_a_representative`, `unknown_kvk`, `attestation_invalid` |
| `bootstrapped_at` | timestamptz NULL | set on `active` |
| `created_at` / `updated_at` | timestamptz | |

Partial unique index: one live instance per `(requestor_user_id, kvk_number)` where status
in (`provisioning`,`awaiting_attestation`) — prevents duplicate in-flight opens. A
`rejected` instance is retained for the audit/evidence trail (it never became an org).

### 4.2 `wallet_representations` — the mandate list (Art 5(1)(j), Art 6(2))

Created when the attestation arrives (that's when we know the representatives).

| column | type | notes |
|---|---|---|
| `id` | uuid PK | |
| `wallet_instance_id` | uuid FK→wallet_instances | stable from creation; `ON DELETE CASCADE` |
| `organization_id` | uuid FK→organizations | set at activation |
| `kind` | text CHECK | `bestuurder` \| `gevolmachtigde` \| `overig` |
| `given_names` | text | from the attestation |
| `family_name` | text | from the attestation |
| `date_of_birth` | date NULL | from the attestation — used for co-owner claim matching (§8) |
| `authority` | text CHECK | `sole` \| `jointly` — informs owner grade |
| `valid_from` / `valid_until` | timestamptz NULL | expiry/over-delegation detection (Art 6(2)) |
| `claimed_by_user_id` | uuid FK→users NULL | set when a person claims this representation |
| `claimed_at` | timestamptz NULL | |
| `revoked_at` | timestamptz NULL | soft-revoke keeps the trail |
| `source_message_id` | uuid FK→qerds_messages | the attestation that asserted it |
| `created_at` / `updated_at` | timestamptz | |

Membership stays the existing `organization.Membership`. On claim: create/reuse the `users`
row, `organization.AddMembership` with `RoleAdmin` (bestuurder) or a scoped role
(gevolmachtigde), set `claimed_by_user_id`. Representation is source of truth; membership is
the derived access grant.

---

## 5. State machine

```
wallet_instances:
  (open wallet) ──────────────────────────▶ provisioning   (address generated)
  provisioning ──(PID+KVK sent to KVK)─────▶ awaiting_attestation
  awaiting_attestation ──(attestation: requester IS a representative)──▶ active
  awaiting_attestation ──(requester NOT a representative)─────────────▶ rejected
  awaiting_attestation ──(unknown KVK / invalid attestation / timeout)▶ rejected

on `active` (single tx): create organization (identity from attestation)
                         + link wallet_instance + insert representations
                         + first-owner membership (requester) + mark requester's rep claimed

  active ──(owner request | compromise | cessation)──▶ suspended | revoked   (Art 6(2))
```

---

## 6. Flows

### 6.1 Open the wallet (central, slug-free — org doesn't exist yet)

1. Person is logged in via OpenID4VP (passport|id-card + email + phone) → global `user`,
   with verified identity claims (the **PID**) available.
2. `POST /api/v1/wallet {kvkNumber}` (behind `auth.RequireUser`). `wallet.Service`:
   creates `wallet_instances(provisioning)`, provisions its QERDS **digital address** via
   the qerds address store, then calls `registryprovider.Client.RequestRegistration(ctx,
   pid, kvkNumber)` which sends the **outbound QERDS message** carrying the PID + KVK number
   to KVK's address → status `awaiting_attestation`, storing `request_message_id`. Returns
   `202 {id, status}` to poll.

### 6.2 KVK attestation delivery & activation (async, inbound)

3. KVK delivers the **registration attestation over QERDS** → existing `qerds/webhook.go`
   stores the message + `qerds_evidence`.
4. The wallet slice recognises content type `nl.kvk.registration-attestation.v1`, correlates
   it to the pending instance by request reference, and parses
   `{legalName, euid, kvkNumber, representatives[], requesterIsRepresentative}`.
   - **KVK confirms the requester is a representative** → single tx:
     `organization.Store.Create` (slug derived from the *attested* legal name,
     collision-suffixed — we only now know the name), link `organization_id` onto the wallet
     instance, populate `legal_name`/`euid`, insert all `wallet_representations`, mark the
     requester's representation `claimed`, `organization.AddMembership(requester, admin)`,
     set instance `active` + `bootstrapped_at`. Every write audited in-tx.
   - **KVK does not confirm** (requester not a representative / unknown KVK / invalid) →
     instance `rejected` with `reject_reason`; no org created. The requester is told they
     must be a listed representative.

### 6.3 Claimable owners — the other representatives (no new KVK round-trip)

5. The co-representatives from the attestation exist as **unclaimed representations**. A
   co-director logs in via OpenID4VP (their own passport/id-card) and is offered the
   unclaimed representation(s) matching their identity to **claim** — reusing the
   `organization/accept.go` accept-via-presentation shape: match → `AddMembership` + mark
   representation claimed + audit.
   - **The attestation already establishes the authorized set** (KVK asserted it, with
     evidence); the claim only binds a login to one listed representative. See §8 for how
     strongly the login is matched to the representation entry.

### 6.4 Revocation (Art 6(2))

6. Owner-initiated or compromise/cessation: `wallet_instances.status → suspended|revoked`;
   representations soft-revoked (`revoked_at`); dependent memberships handled by the existing
   last-admin guard (`organization.ErrLastAdmin`). External-validator propagation is out of
   scope for v1 (§11).

---

## 7. HTTP API

Return `*respond.APIError` for client errors; wrap handlers in `respond.HandlerFunc`.

**Central (slug-free, `auth.RequireUser` only):**
- `POST /api/v1/wallet` `{kvkNumber}` → `202 {id, status}` (open a wallet)
- `GET /api/v1/wallet/{id}` → poll `{status, organizationSlug?, rejectReason?}`

**Org-scoped (`requireUser ∘ organization.Handler.Authorize`, org via `OrgFromContext`):**
- `GET /api/v1/orgs/{slug}/wallet` → instance: verified identity + evidence link
- `GET /api/v1/orgs/{slug}/wallet/representations` → mandate list (claimed/unclaimed)
- `POST /api/v1/orgs/{slug}/wallet/representations/{id}/claim` → claim (via OpenID4VP)
- Admin-gated (`+ RequireOrgAdmin`): `POST .../wallet/suspend`, `POST .../wallet/revoke`,
  `POST .../wallet/representations/{id}/revoke`

**Inbound attestation** is not a new public route — it arrives through the existing QERDS
inbound webhook; the wallet slice consumes recognised messages.

---

## 8. Trust & identity matching

- **Registrant (first owner): matched by KVK.** We send the PID + KVK number; KVK matches
  the natural person against its register and asserts the result. Strongest possible — the
  authentic source decides, with QERDS evidence. No local guessing.
- **Co-owner claims: matched locally against the attested representation** on **name +
  date of birth** from the claimant's passport/id-card (both present in the pbdf staging
  identity credential; DOB stored on the representation from the attestation). This is
  materially stronger than name-only (the earlier draft's weak point): the authorized *set*
  is KVK-attested; the claim binds a verified identity credential to one listed entry.
  - **Residual assumption to validate** (per `.ai/conventions` §Assumptions): the pbdf
    staging passport/id-card exposes `documentNumber`/name/DOB but **not BSN**, and KVK's
    representative data keys on BSN/name/DOB — so co-owner matching is name+DOB, not a shared
    strong identifier. If KVK includes a shared pseudonymous person reference in the
    attestation and the credential can disclose it, upgrade co-owner matching to that. Track
    in §12.
- **Evidence chain:** `wallet_instances.attestation_message_id` +
  `wallet_representations.source_message_id` → `qerds_messages` → `qerds_evidence`
  (append-only). Every owner traces to the registered delivery that created them.
- **Audit:** all mutations recorded in-tx via the `audit` seam. New constants in
  `internal/audit/audit.go`: actions `WalletOpened`, `WalletBootstrapped`,
  `RepresentationClaimed`, `RepresentationRevoked`, `WalletSuspended`, `WalletRevoked`;
  targets `TargetWalletInstance`, `TargetRepresentation`. Standard `{before, after}`
  envelope; store readable values, never ids.

---

## 9. `registryprovider` seam (dev bench)

Mirror `qerdsprovider` (external client + swappable drivers):
- Interface `Client`: `RequestRegistration(ctx, pid PID, kvkNumber string) (requestRef, error)`.
- `stub` driver (dev): deterministic fake org + 2–3 representatives via `gofakeit` (fixed
  seed, per `internal/seed` — CSPRNG only for secrets, never demo data), and, to exercise
  the real inbound path, posts a synthetic `nl.kvk.registration-attestation.v1` QERDS
  message to the inbound webhook after a short delay (including a `requesterIsRepresentative`
  flag driven by the stubbed rep list vs. the requester's PID name). Makes the async
  two-legged flow runnable end-to-end without KVK.
- `bris` driver (later): real KVK/BRIS request; the attestation returns as a genuine QERDS
  delivery. Chosen by config, not code (like `qerdsprovider` stub↔domibus).
- Config (`internal/config`): KVK's QERDS address, attestation content type, provider driver
  selector. No hardcoded hosts/addresses.

---

## 10. Frontend (design only)

3-part pattern (`frontend/src/api/*.ts` client + `*.queries.ts` hooks + route); keys nested
under the org detail key so org invalidation cascades:
- **"Open a business wallet"** (requires OpenID4VP login): enter KVK number → `POST /wallet`
  → poll → on `active`, redirect into the new org; on `rejected`, show the reason.
- **Org wallet card** (dashboard): verified identity (name / EUID / KVK), status, evidence link.
- **Representations view**: mandate list with claimed/unclaimed badges; unclaimed rows
  matching the viewer offer "claim your ownership" (OpenID4VP presentation); admin revoke.
- i18n under `wallet.*`; the not-a-representative rejection uses the existing
  `suppressErrorToast` inline-error convention.

---

## 11. Scope

**In (v1 design):** provisioning-first "open a wallet" that generates its QERDS address;
async two-legged QERDS {PID+KVK}→attestation with a stub driver; KVK-side authorization of
the requester; org + representations + first-owner materialization on activation; claimable
co-owners (name+DOB match, no new round-trip); basic suspend/revoke; full audit + evidence
linkage.

**Out (v1):** real KVK/BRIS integration (stub only); a shared strong person reference for
co-owner matching (name+DOB for now); the European Digital Directory / SMP-SML address
resolution (KVK address from config; reuses the qerds `ResolveAddress` seam when it lands);
revocation propagation to external validators; joint-authority quorum enforcement beyond
recording it.

## 12. Open questions

1. **Shared person reference** — will KVK include a pseudonymous person id in the
   attestation that the passport/id-card can also disclose, to make co-owner matching a
   shared strong identifier rather than name+DOB? (Gates the §8 upgrade; ties to the PID
   attribute set in `.ai/features/auth-openid4vp.md`.)
2. **Outbound request contents** — does KVK want the raw PID presentation or our
   verifier-verified identity claims in the QERDS payload? (KVK decides eventually; QERDS is
   the channel either way.)
3. **Joint authority** — do `jointly`-authorised bestuurders need a quorum for wallet
   actions, or only to open? Recorded now; enforcement deferred.
4. **Re-attestation / drift** — cadence for refreshing the mandate list from KVK, and how
   departed directors' memberships are reconciled when a newer attestation arrives.
5. **Provisioning timeout** — TTL for `awaiting_attestation` before `rejected`, and whether
   the provisioned address is released.

## 13. Done when (if built)

- `POST /wallet` creates a `provisioning` instance with a QERDS address; no `Organization`
  exists until KVK's attestation confirms the requester; the reject path leaves zero org
  rows and a retained `rejected` instance (integration test).
- The stub registry driver drives a full open→{PID+KVK}→attestation→activation through the
  real QERDS inbound path (integration test against `TEST_DATABASE_URL`).
- Every wallet mutation is audited in-tx; `wallet_instances` and every
  `wallet_representations` row link back to a `qerds_messages` + `qerds_evidence` chain.
- Co-owner claim reconciles a passport/id-card identity (name+DOB) to an unclaimed
  representation and creates a membership; last-admin/expiry guards hold.
- Backend + frontend verify sequences (`CLAUDE.md`) pass.

## Harvest

- Convention to add/update: none beyond these docs (reuses existing seams).
- Feature docs: **this file** + `.ai/features/auth-openid4vp.md` (PID seam); cross-link from
  `.ai/features/qerds.md` (shared provider-seam stance). Promote from Design to built on merge.
