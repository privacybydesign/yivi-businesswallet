# OpenID4VP inbound invocation and wallet-provider discovery

Status: **implemented** on this branch — the invocation/routing seam below is built; the
durable description is `.ai/features/openid4vp-inbound.md`. Issue
[#188](https://github.com/privacybydesign/yivi-businesswallet/issues/188), the
invocation/discovery slice of the OpenID4VP-holder epic
[#112](https://github.com/privacybydesign/yivi-businesswallet/issues/112).
Sibling slices: [#111](https://github.com/privacybydesign/yivi-businesswallet/issues/111)
(DC API on the requestor side, unrelated protocol role), #112 itself (the presentation
crypto this design's seam calls into), [#113](https://github.com/privacybydesign/yivi-businesswallet/issues/113)
(org-wallet consent — *who* may let a presentation go out).

## Why

The repo only speaks OpenID4VP as a **requestor**: `internal/openid4vpverifier`'s package
doc says outright "our backend is a requestor/orchestrator... [it] does NOT implement the
verifier role" — it starts a presentation at a **hosted, remote** EUDI verifier and polls it
by `transaction_id` (`.ai/features/auth-openid4vp.md`). The `openid4vp://` link that flow
emits, and its rewrite to a Yivi mobile-wallet universal link
(`frontend/src/ui/identity-disclosure.tsx:24-28`), is how *this backend* invokes a **natural
person's** device wallet. Nothing in the repo lets an **external verifier** invoke the
**business wallet itself** as a holder/presenter — no inbound endpoint, no transaction
store shaped for it, no wallet metadata, no frontend route. This is greenfield (confirmed by
grep: no hits for wallet metadata, `.well-known` authorization-server metadata, an inbound
transaction concept, or DCQL outside the existing outbound package).

That gap blocks #112 (org credentials can't be presented to anyone who can't reach an
addressable wallet endpoint) and is itself non-trivial: `openid4vp://` is a shared scheme a
browser-hosted, multi-tenant wallet cannot rely on owning, and a verifier cannot render one
button per organization. This design fixes the **invocation and routing** seam — one stable
HTTPS endpoint per deployment, an opaque transaction lifecycle, org selection gated behind
real auth, and published wallet metadata — without building the presentation crypto (#112)
or the consent/approval policy (#113) it hands off to.

## Architecture

```
 verifier (any)                     browser                          this backend
      │                                │                                   │
      │  redirect / QR ───────────────▶│  GET /openid4vp?client_id=...    │
      │                                │  (SPA route, served by the       │
      │                                │   existing spaHandler fallback)  │
      │                                │──POST /api/v1/openid4vp/start───▶│ validate + fetch
      │                                │◀─────────{id: opaque}────────────│ request object,
      │                                │                                   │ persist transaction
      │                                │  [not authenticated]              │
      │                                │──▶ /login?returnTo=/openid4vp/{id}│
      │                                │  [authenticated]                  │
      │                                │──GET /api/v1/openid4vp/{id}/orgs─▶│ organization.
      │                                │◀────[{slug,name}, ...]───────────│ Store.ListForUser
      │                                │  user picks org                  │
      │                                │──POST /orgs/{slug}/openid4vp/    │ Authorize (existing
      │                                │        {id}/select ─────────────▶│ tenant seam) →
      │                                │                                   │ eudiholder.Present
      │                                │                                   │ (#112) → #113 gate
      │◀──────────────────── direct_post response ───────────────────────│
```

The opaque id is the same shape as the outbound flow's session id
(`internal/presentation.Store`, `.ai/features/auth-openid4vp.md` §2): a random,
server-minted id that the client carries, never the sensitive value it maps to. There the
sensitive value is the verifier's `transaction_id`; here it is the raw inbound request
(`client_id`, `request_uri`, the fetched-and-validated Request Object) — arguably more
sensitive, since it also carries the verifier's identity and the DCQL query.

### Root-mux vs `/api/v1` — two different "public" endpoints, on purpose

`internal/server.New` mounts every domain slice's `Registerer.Register(*http.ServeMux)`
under `/api/v1` (stripped) and falls through to the SPA (`spaHandler`) for everything else
when `staticDir != ""` (`internal/server/server.go:34-59`). `/livez`, `/readyz` and
`apidocs.Register(root)` are the existing precedent for registering something on the
**root** mux instead, "not mistaken for API resources" (`server.go:40-43`).

This design needs both shapes, for different reasons:

- **`GET /openid4vp`** (the address a verifier redirects to or a QR encodes) is a **browser
  navigation**, not an API call — it must render UI (login-if-needed, then an org picker).
  It needs **no backend route at all**: the existing SPA fallback already serves `index.html`
  for any unmatched root path in production, and a new top-level React Router entry
  (`{ path: "/openid4vp", Component: OpenID4VP }`, a sibling of `Login`/`Claim` in
  `frontend/src/router.tsx:121-125`, *not* nested under `ProtectedRoute`) reads
  `location.search` client-side and drives the flow from there. In dev, Vite serves the
  frontend directly, so the same route works unchanged.
- **`GET /.well-known/oauth-authorization-server`** (wallet metadata, decision 4 below) is
  fetched by verifier *software*, not a browser — it must return real JSON, so it is a
  genuine backend handler, registered on the **root** mux like `apidocs.Register(root)`, not
  under `/api/v1` (a metadata document has no version and is not an API resource) and not
  caught by the SPA fallback (it must not return `index.html` on a `curl`).
- **`POST /api/v1/openid4vp/start`**, **`GET /api/v1/openid4vp/{id}/status`**,
  **`GET /api/v1/openid4vp/{id}/orgs`** are ordinary public/authenticated JSON endpoints
  under `/api/v1`, exactly like `auth`'s `/auth/session*` routes
  (`internal/auth/handler.go:41-45`) — the new slice's `Register(*http.ServeMux)` plugs into
  `main.go`'s existing `server.New(pool, cfg.StaticDir, ...)` call like every other feature.
- **`POST /orgs/{slug}/openid4vp/{id}/select`** is org-scoped and composes the existing
  tenant seam unchanged: `auth.RequireUser` → `organization.Handler.Authorize` (resolves
  `{slug}`, membership, role, authority — `internal/organization/middleware.go:20-63`),
  registered from the new slice the same way `qerds`/`wallet`/etc. take `requireUser` and
  `orgHandler.Authorize` as constructor params (`BACKEND.md` "Tenant access seam").

### `returnTo` through login does not exist today — new, not reused

The issue says "preserve only that opaque ID through the login redirect." There is
**no `returnTo` mechanism in the repo to reuse**: `ProtectedRoute` unconditionally
`<Navigate to="/login" replace>`s with no query param
(`frontend/src/routes/protected-route.tsx:11-13`), and `Login` unconditionally
`navigate("/")`s on success (`frontend/src/routes/login.tsx:101,136`). This design adds it:

- `/openid4vp` is **not** nested under `ProtectedRoute` (that component has no way to carry
  a return target); it calls `useMeQuery` itself and, when unauthenticated, navigates to
  `/login?returnTo=/openid4vp/{opaqueId}` — the opaque id only, never `client_id`/`request_uri`.
- `Login` learns to read `returnTo` and navigate there instead of `/` on success —
  **guarded**: accept only a same-origin relative path matching `^/openid4vp/[A-Za-z0-9_-]+$`
  (the one caller that needs it), reject anything else and fall back to `/`. An unchecked
  `returnTo` is an open-redirect-shaped bug the moment it accepts an absolute URL; scoping
  the regex to this one route removes the class rather than sanitizing generically.
- Resuming with just the id means `GET /api/v1/openid4vp/{id}/status` must report enough to
  redraw the right step (e.g. `needs_org_selection` vs `expired`) — the transaction row is
  the state machine, not the URL.

## Decisions

### 1. Transaction store — new slice, not a reuse of `internal/presentation`

`internal/presentation` is purpose-built for the *outbound* id↔id mapping and is a
dependency of `auth`; reusing it here would mean an unrelated domain reaching into `auth`'s
private store. New package **`internal/openid4vppresenter`** (mirrors `openid4vpverifier`
naming; "presenter" is the issue's own term for this role), anchored on the **with-service**
template (`auth` is the precedent — `BACKEND.md` "Layering") since the flow orchestrates the
transaction store, `organization.Store.ListForUser`, `eudiholder.Holder`, and `audit`:

```
handler → service → store (openid4vp_transactions) + organization + eudiholder + audit
```

`openid4vp_transactions` (new migration, `internal/migrate/migrations/`, next free
timestamp after `20260908090200` per the existing convention):

| column | notes |
|---|---|
| `id_hash` | `BYTEA UNIQUE NOT NULL` — same shape as `presentation_sessions.id_hash`; only the hash is stored, the raw opaque id is client-held |
| `client_id`, `request_uri`, `request_uri_method` | the inbound params as received |
| `verifier_identity` | resolved from the validated Request Object's `client_id` binding (decision 5) — what the org-selection UI shows as "who is asking" |
| `dcql_query` | `JSONB` — the fetched, validated Request Object's query, so a slow org-picking human does not require re-fetching a `request_uri` that may be single-use |
| `nonce`, `response_uri`, `response_mode` | from the validated Request Object; needed to build the eventual `direct_post`/`direct_post.jwt` response |
| `status` | `pending_auth` → `org_selected` → `completed` / `denied` / `expired` — enum-shaped, code-defined like `audit`'s action constants, not free text |
| `organization_id` | `UUID NULL REFERENCES organizations(id)` — set only after decision 2 (org selection); null before that |
| `user_id` | `UUID NULL REFERENCES users(id)` — set once authenticated, before org selection |
| `expires_at` | short TTL (config, mirrors `PresentationTTL` / `PRESENTATION_SESSION_TTL` — `internal/config/config.go:24,176,376`; propose a new `OPENID4VP_TRANSACTION_TTL`, default on the order of minutes, shorter than the 15m outbound default since this spans an interactive multi-step flow but must not outlive a plausible browser session) |
| `consumed_at` | `TIMESTAMPTZ NULL` — set on `completed`/`denied`; a second `/select` or a resumed `/status` past this point is rejected, enforcing **one-time use** (issue requirement) without a separate table |

No `DELETE ... RETURNING`-style reuse of the row after `consumed_at` is set — the issue's
"never treat an organization hint... as authorization" means the `/select` handler always
re-derives the org from the authenticated session + membership, never from a client-supplied
field, exactly like `organization.Handler.Authorize` already does for every other org-scoped
route.

### 2. Org selection after auth — reuse, not new authz

Once `POST /api/v1/openid4vp/start` returns the opaque id and the user is authenticated,
`GET /api/v1/openid4vp/{id}/orgs` calls the existing `organization.Store.ListForUser(ctx,
userID)` (`internal/organization/store.go:177`, already the "my orgs" query used by
`GET /orgs`) — **no new "which orgs can this user pick from" logic**. The user picks one,
`POST /orgs/{slug}/openid4vp/{id}/select` runs behind the standard `Authorize` tenant seam,
which is where "no provider URL or verifier button per organization" (issue, acceptance
criteria) is enforced structurally: the org slug never appears anywhere in the public
`/openid4vp` surface, only after the picker, scoped by membership like every other org route.

### 3. `Present` seam on `eudiholder.Holder` — sketched, not implemented

`eudiholder.Holder` (`internal/eudiholder/eudiholder.go:36-96`) currently has no
outbound-presentation method — `Store`/`Redeem`/`Claims`/`Displays`/`Validities` are all
receive/read shaped. This design fixes the seam's **shape** so `openid4vppresenter.Service`
can be written and tested against it now, while #112 supplies the real DCQL-match +
selective-disclosure + WSCA holder-binding implementation
(`.ai/features/wsca-holder-binding.md` is the key-binding precedent it reuses):

```go
// Present builds a vp_token satisfying dcqlQuery from orgID's held credentials, key-bound
// for audience/nonce. #112 implements the DCQL match and SD-JWT VC presentation; this seam
// only fixes the call shape the inbound slice is written against.
Present(ctx context.Context, orgID uuid.UUID, dcqlQuery []byte, nonce, audience string) (Presentation, error)
```

`StubHolder` gets a matching stub (returns `ErrNotConfigured` or a canned presentation, same
pattern as its other methods) so `openid4vppresenter` has something to run integration tests
against without #112 landing first. The **real** method body — DCQL evaluation, disclosure
selection, KB-JWT signing — is explicitly #112's, not built here.

### 4. Wallet metadata

`GET /.well-known/oauth-authorization-server` (RFC 8414's default well-known path; OpenID4VP
§10 wallet metadata is shaped as OAuth AS metadata because the wallet plays the "authorization
server" role toward the verifier-as-client in this exchange), registered on the root mux,
unauthenticated, `Cache-Control` set (it changes only on deploy):

```json
{
  "issuer": "{cfg.AppBaseURL}",
  "authorization_endpoint": "{cfg.AppBaseURL}/openid4vp",
  "vp_formats_supported": {"dc+sd-jwt": {"sd-jwt_alg_values": [...], "kb-jwt_alg_values": [...]}},
  "client_id_prefixes_supported": ["x509_san_dns"]
}
```

`issuer`/`authorization_endpoint` derive from `cfg.AppBaseURL` (already validated absolute
HTTP(S) — `internal/config/config.go:433-434`), never hardcoded. `vp_formats_supported`'s
algorithm lists and `client_id_prefixes_supported` must be **read from the configured holder
and verifier-trust capabilities**, not asserted — the issue is explicit that the example is
not a new hardcoded capability claim. Concretely: the algorithm the WSCA/irmago holder-binding
path actually signs with (`wsca-holder-binding.md`), not a literal `"ES256"` typed here.

### 5. Security constraints — what each maps to concretely

The issue's list, each pinned to a real mechanism rather than restated as prose:

- **Accept the supported request forms, reject ambiguous combinations** — decision 1's
  transaction schema only has columns for the pass-by-reference form (`client_id`,
  `request_uri`, `request_uri_method`); this design supports that form only, not a by-value
  `request` JAR parameter. The handler behind `POST /api/v1/openid4vp/start` rejects, before
  persisting a transaction row, a request that supplies `request` instead of `request_uri`,
  supplies both, or supplies neither — the standard OAuth `invalid_request` error, never a
  silent guess at which one wins.
- **`request_uri_method` — supported values or the standard error** — OpenID4VP profiles
  `get` and `post`. This repo's own outbound requests always send `get`
  (`openid4vpverifier/client.go:21,74`), and `verifier_test.go:164` documents that irmago's
  wallet GETs the request object and ignores this field entirely — there is no existing
  precedent in this repo for driving a `post` fetch. The inbound handler validates
  `request_uri_method` against the values it can actually execute and returns the profile's
  standard unsupported-method error for anything else, rather than ignoring it the way the
  outbound side's counterpart does. Whether `eudiholder`'s irmago-backed engine (decision 3)
  can itself drive a `post` fetch is a #112 implementation question this seam does not
  resolve; the seam only fixes that the check exists and what an unsupported value returns.
- **HTTPS outside dev** — `internal/config` already has this exact shape of escape hatch for
  another provider: `AttestationHolderAllowInsecureHTTP` /
  `ATTESTATION_HOLDER_ALLOW_INSECURE_HTTP` (`config.go:67,282,494-495`), defaulting closed.
  Mirror it for the `request_uri` fetch: reject non-`https://` unless an equivalent dev-only
  flag is set.
- **SSRF / redirect / size / timeout guards on the `request_uri` fetch** — new: nothing in
  the repo fetches an *externally supplied* URL server-side today (`openid4vpverifier`'s
  `Client` only ever calls its own configured `baseURL`). Needs its own `http.Client` with a
  disabled or allowlist-checked redirect policy, `io.LimitReader` (the outbound client's
  `bodyLimit` constant, `openid4vpverifier/client.go:25`, is the size-cap precedent), and a
  request timeout via context.
- **One-time, short-lived transaction ids + nonce/replay** — `expires_at` +
  `consumed_at` on `openid4vp_transactions` (decision 1); the nonce is part of the persisted,
  validated Request Object, checked once at response-build time, never regenerated.
- **Signed Request Object validation, `client_id` binding** — new crypto surface; out of this
  design's implementation (belongs with #112's holder crypto), but the *seam* is fixed: the
  service validates before persisting `verifier_identity`/`dcql_query`, so nothing
  unvalidated ever reaches the org-picker UI.
- **Never log request objects, disclosed claims, credentials, response URIs** — matches the
  existing posture in `.ai/features/auth-openid4vp.md` §7 ("never expose `transaction_id`");
  same discipline, applied to `dcql_query`/`response_uri` here.
- **Audit only after #113's governance decision** — see Out of scope.

## New audit vocabulary (`internal/audit/audit.go`)

No presentation/disclosure actions exist yet (confirmed by grep). Add, alongside the existing
`Attestation*` block:

```go
PresentationRequested  = "presentation.requested"   // transaction created, pre-auth
PresentationOrgSelected = "presentation.org_selected"
PresentationCompleted  = "presentation.completed"    // response sent
PresentationDenied     = "presentation.denied"       // rejected by validation or #113's gate
PresentationExpired    = "presentation.expired"
...
TargetPresentationTransaction = "presentation_transaction"
```

Written inside the same `database.InTx` as the transaction row's state transition
(`BACKEND.md` "Auditing"), actor from `audit.ContextWithActor`, metadata excludes
`dcql_query`/`response_uri`/tokens per the no-sensitive-material rule above.

## Out of scope / deferred

- **The presentation crypto itself** — DCQL matching, selective disclosure, KB-JWT signing
  behind `Holder.Present` (#112).
- **Consent/approval policy** — *who* may let a selected org's `/select` actually produce and
  send a `direct_post` response is #113's governance layer. **That layer's code is not in
  `main`** — `BACKEND.md`'s explicit gotcha: PRs #128/#129 (RBAC `RequirePermission` +
  consent) never reached `main` after #117's design landed, only `.ai/plans/rbac-model.md`
  did. So until #113 ships, `/select` can validate, persist org selection, and (dev-only,
  behind a flag) complete a presentation for testing — but it must not silently ship an
  auto-approve default that becomes the de facto policy.
- **Global wallet-provider registry / chooser** — explicitly out of scope in the issue; not
  designed here beyond noting the integration seam (decision 4's metadata document is exactly
  what a future chooser would read).
- **Registering as an OS-native Digital Credentials API provider** — separate transport,
  #111's territory, not this.
- **Changing `internal/openid4vpverifier` or `identity-disclosure.tsx`** — outbound, opposite
  protocol role, untouched.
- **Signed Request Object validation cryptography** (cert-chain / `x509_san_dns` resolution) —
  the seam is fixed (decision 5); the implementation is #112-adjacent, not here.

## Decisions settled

- **Package name:** `internal/openid4vppresenter` (new), distinct from `openid4vpverifier`
  (outbound) and `eudiholder` (credential store + `Present` crypto, #112).
- **Routing split:** `/openid4vp` itself needs no backend route (SPA fallback); wallet
  metadata is a real root-mux JSON handler; start/status/orgs/select are ordinary
  `/api/v1` routes, the last composing the existing tenant seam unchanged.
- **`returnTo` is new infrastructure**, not a reuse of an existing mechanism, scoped to a
  single allowlisted path pattern to avoid an open redirect.
- **One-time use** is `consumed_at` on the transaction row, not a separate table.
- **Metadata well-known path:** `/.well-known/oauth-authorization-server` (RFC 8414 default).

## Done when (design review checklist)

- Every acceptance criterion in issue #188 is either satisfied by a concrete mechanism named
  above, or explicitly assigned to #112/#113 as a dependency — none silently dropped.
- The root-mux-vs-`/api/v1`-vs-SPA routing split is unambiguous enough that an implementing
  branch does not have to make that call itself (decision on where each of the five endpoints
  lives is written above, with file/line precedent for each).
- The transaction schema, TTL/one-time-use enforcement, and the security-constraint list each
  map to a named table/column/config flag/existing precedent, not a restatement of the issue.
- The `Present` seam signature is fixed enough for #112 to implement against without
  redesigning this slice's caller.
- Nothing in this document implements #112's crypto or #113's consent policy.

## Harvest

- Convention to add/update in `.ai/conventions/`? **Yes** — `BACKEND.md` "Routing & HTTP"
  gains the `server.RootRegisterer` note (a feature that also serves a well-known document
  outside `/api/v1`, and how to keep the apidocs coverage scan from mistaking it for an API
  route); `FRONTEND.md` "Structure & Patterns" gains the `returnTo` allowlist rule.
- Feature doc to write/update in `.ai/features/`? **Yes** — `.ai/features/openid4vp-inbound.md`
  (this plan's durable form; cross-referenced from `auth-openid4vp.md`'s role as the outbound
  counterpart). This plan stays the design record for #188; #112 and #113 build against the
  seams it fixed.
