# Feature: Inbound OpenID4VP — the business wallet as holder/presenter

**Status:** Implemented — the invocation and routing seam (issue
[#188](https://github.com/privacybydesign/yivi-businesswallet/issues/188)) and the holder
cryptography of the OpenID4VP-holder epic
([#112](https://github.com/privacybydesign/yivi-businesswallet/issues/112)): signed
Request Object verification, DCQL matching over the org's held credentials, selective
disclosure with a key-bound SD-JWT VC presentation, `direct_post` and encrypted
`direct_post.jwt` responses. The consent/approval policy
([#113](https://github.com/privacybydesign/yivi-businesswallet/issues/113)) remains a seam.
Design of record: `.ai/plans/openid4vp-invocation.md`. Local end-to-end testing: §8.
**Counterpart:** `.ai/features/auth-openid4vp.md` is the *outbound* role — this backend as a
requestor asking a natural person's device wallet for a login disclosure. This file is the
opposite role: an **external verifier** invoking the **business wallet itself** to present an
**organization's** credentials.

---

## 1. What exists

| Piece | Where |
|---|---|
| Domain slice (handler → service → store + fetcher + validator + responder) | `backend/internal/openid4vppresenter/` |
| Transaction table | `openid4vp_transactions` (migration `20260909090000`) |
| `Present` on the holder engine (DCQL match, selective disclosure, KB-JWT) | `eudiholder.Engine.Present` (`engine_present.go`), `eudiholder.Formats()` |
| Verifier / issuer trust without per-org storage | `eudiholder.NewVerifierTrust`, `eudiholder.NewIssuerTrust` (`verifiertrust.go`) |
| Local relying party for dev and tests | `internal/devverifier` (identity, JAR signing, response verification), `cmd/devverifier` (HTTP), `dev-setup/devverifier/` (checked-in dev chain) |
| Audit vocabulary | `audit.Presentation*`, `audit.TargetPresentationTransaction` |
| Wallet metadata | `GET /.well-known/oauth-authorization-server` (root mux, via `server.RootRegisterer`) |
| Browser entry point | SPA routes `/openid4vp` and `/openid4vp/:id` (`frontend/src/routes/openid4vp*.tsx`) |
| Login return target | `frontend/src/lib/return-to.ts` (`safeReturnTo`, `loginPathFor`) |

## 2. Flow

```
 verifier                         browser (SPA)                         backend
    │ redirect / QR ──────────────▶ GET /openid4vp?client_id&request_uri
    │                                │──POST /api/v1/openid4vp/start────▶ reject ambiguous forms,
    │                                │◀───────────{id}───────────────────  fetch request_uri (guarded),
    │                                │ replace URL → /openid4vp/{id}       validate, persist, audit
    │                                │ [no session] → /login?returnTo=/openid4vp/{id}
    │                                │──GET /api/v1/openid4vp/{id}/orgs──▶ bind user, ListForUser
    │                                │──POST /orgs/{slug}/openid4vp/{id}/select──▶ tenant seam →
    │                                │                                      org_selected (+audit)
    │                                │                                      [AUTO_PRESENT] Present →
    │◀──────── direct_post (vp_token, state) ─────────────────────────────  direct_post → completed
    │──────── {redirect_uri} ───────▶ "Return to verifier"
```

- **Only the opaque id leaves the backend.** `client_id`, `request_uri`, the Request Object,
  the DCQL query, nonce and `response_uri` live in the row; the browser, the login redirect
  and the audit log see the verifier's identity at most. `status` is public (the id is the
  bearer, like the outbound session status) and reports `status` + `verifier` only.
- **Routing split** (why three shapes): `/openid4vp` is a browser navigation → SPA fallback,
  no backend route. The well-known document is fetched by software → root mux, real JSON,
  never `index.html`. `start`/`status`/`orgs`/`select` are `/api/v1` routes; `select`
  composes `auth.RequireUser → organization.Handler.Authorize` unchanged, so the org slug is
  authorization-checked against membership and never appears on the public surface.
- **One-time use** is `consumed_at` on the row; `EffectiveStatus` applies expiry on read so a
  resumed browser sees `expired` before the pruner does. The pruner (`Store.Prune`, on the
  shared `SESSION_PRUNE_INTERVAL`) marks and audits expiries, then deletes consumed rows
  after an hour.
- **A transaction is bound to the first authenticated user** who resumes it (`user_id`);
  another user with the same id gets 403.

## 3. Request Object validation

`openid4vppresenter.Validator` is what the service persists through. Two implementations:

- `VerifyingValidator` — **the default.** Wraps irmago's
  `RequestorCertificateStoreVerifierValidator`, the code the Yivi wallet runs: JWS signature
  against the `x5c` leaf, the leaf's chain against the verifier trust with the `client_id`'s
  DNS name as the required SAN (`x509_hash` too), revocation, and — for a Yivi-issued
  relying-party certificate — that the DCQL query stays inside the credentials the
  certificate authorizes. Identity shown to the org picker: the certified legal name, else
  `client_metadata.client_name`, else the certificate subject.
- `UnverifiedDecoder` — dev / CI only, behind
  `OPENID4VP_PRESENTER_ALLOW_UNVERIFIED_REQUEST_OBJECTS=true`. Structural checks only
  (compact JWS, `alg` not `none`, `client_id` equality, `exp`).

Both then apply the protocol constraints this slice can answer (`checkFields`):
`response_type=vp_token`, `response_mode` `direct_post` or `direct_post.jwt` (the latter
only with `client_metadata.jwks`), `response_uri` under the URL policy, URL-safe nonce,
non-empty DCQL. The validated JAR is stored whole (`request_object`) because the response
step needs the verifier's encryption keys from it.

**Verifier trust** is deployment-level (a request is validated before any org is known), so
it is not irmago's per-org storage-backed trust model: `eudiholder.NewVerifierTrust` builds a
static context from irmago's pinned Yivi relying-party anchors (production always, staging
under `ATTESTATION_HOLDER_STAGING_ANCHORS`, the one Yivi-PKI switch) plus
`OPENID4VP_VERIFIER_TRUST_CHAIN` (PEM, merged like the holder's partner chain). The Yivi
staging verifier's certificate authorizes only `pbdf-staging.*` credentials, so it cannot ask
a business wallet for organization credentials — that is the certificate's authorization
list at work, not a wallet limitation.

## 4. Fetch and response guards (`httpclient.go`)

Nothing in the repo fetched an externally supplied URL server-side before this. One `Policy`
covers `request_uri` and `response_uri`: https only, no userinfo, DNS resolved and each
address checked against loopback / private / link-local / CGNAT / multicast **at dial time**
(the vetted IP is what is dialed — no rebind window), redirects refused outright, body capped
at 1 MiB (over-cap is an error, not a truncation), 10 s per round trip, header cap 64 KiB.
`OPENID4VP_PRESENTER_ALLOW_INSECURE_HTTP=true` lifts both the scheme and the private-address
rule, because a local verifier is plain http on loopback. Transport errors are stripped of
their URL before they can reach a log line.

`request_uri_method`: absent or `get` → GET; `post` → GET too, per OpenID4VP 1.0 §5.10 ("Wallets
not supporting the post method will send a GET request"); anything else →
`invalid_request_uri_method`. A by-value `request`, both, or neither → `invalid_request`.

## 5. Completion is gated, and the gate is not built

`POST …/select` records `org_selected` and stops. That is where #113's consent/approval layer
decides who may let the presentation go out. `OPENID4VP_PRESENTER_AUTO_PRESENT=true` (dev /
CI) skips the wait: `Holder.Present(orgID, dcql, nonce, audience=client_id)` → response →
`completed`, returning the verifier's `redirect_uri` (validated absolute http(s)) for the
"return to verifier" button. A failure consumes the row as `denied` with the failing step as
reason — one nonce, one attempt. An org that holds nothing satisfying a required part of the
query is `denied` with reason `no_matching_credential` and 422 to the browser.

**`Engine.Present`** (`eudiholder/engine_present.go`) is irmago's holder pipeline driven
headlessly: `eudi_sdjwt_dcql.SdJwtVcDcqlHandler` over the org's storage finds candidates
and builds the disclosure plan; `autoSelect` takes the first held bundle of every required
choice with exactly the claim paths the plan derived, skips optional choices (an autonomous
wallet does not volunteer data), and returns `ErrNoMatchingCredential` otherwise;
`PrepareDisclosure` produces the SD-JWT presentation (`sdjwt.CreatePresentation`) and signs
the KB-JWT (`sd_hash`, nonce, `aud`) through `presentationKeyBinder` — the software binder
over the same `holder_binding_keys` rows the receive flow wrote, or
`holderkeys.NewSignerKeyBinder` over the org's WSCA signer under the `wsca` build tag.
`sdJwtVcQueries` routes every `dc+sd-jwt` query to that handler: irmago would send a
three-part vct like `nl.acme.employee` to the IRMA store, which this backend does not have.

**Responder** (`responder.go`): `direct_post` is a form POST of `vp_token` + `state`;
`direct_post.jwt` encrypts `{vp_token, state}` as a JWE (jwx) to the first usable key in
the stored request's `client_metadata.jwks` (key alg, else ECDH-ES for EC; content
encryption from `encrypted_response_enc_values_supported`, default A128GCM) — the same
choice irmago's wallet makes, whose code is unexported.

`StubHolder.Present` returns one placeholder entry per credential-query id so the HTTP flow
runs offline in CI. `eudiholder.Formats()` reads the algorithm from the JOSE library irmago
signs with, which is what `vp_formats_supported` publishes.

## 6. Config

| Variable | Default | Meaning |
|---|---|---|
| `OPENID4VP_TRANSACTION_TTL` | `5m` | Invocation → response; shorter than the outbound 15 m on purpose |
| `OPENID4VP_PRESENTER_ALLOW_INSECURE_HTTP` | `false` | Dev: http + private-network verifier URLs |
| `OPENID4VP_PRESENTER_ALLOW_UNVERIFIED_REQUEST_OBJECTS` | `false` | Dev: `UnverifiedDecoder` instead of `RefusingValidator` |
| `OPENID4VP_PRESENTER_AUTO_PRESENT` | `false` | Dev: complete right after selection (stands in for #113) |
| `OPENID4VP_VERIFIER_TRUST_CHAIN` | empty | Extra relying-party root(s), PEM content, on top of the pinned Yivi anchors |

`compose.override.yaml` turns on insecure-http and auto-present for the dev stack, keeps
unverified request objects **off**, and inlines the dev verifier's root as the trust chain;
`.env.example` documents the flags as never-on-hosted. `main.go` logs a warning at boot for
each dev flag that is on.

## 7. Frontend

- `/openid4vp` parses the verifier's query (`parseInvocation`), POSTs it once, and
  `replace`s the URL with `/openid4vp/{id}` so nothing verifier-supplied stays in history.
- `/openid4vp/:id` reads `status`; unauthenticated + `pending_auth` → `Navigate` to
  `loginPathFor(id)`. It is deliberately **not** under `ProtectedRoute`, which has no way to
  carry a return target.
- `Login` reads `returnTo` and navigates there instead of `/` — through `safeReturnTo`, which
  accepts only `^/openid4vp/[A-Za-z0-9_-]+$` and falls back to `/` for everything else. This
  is the repo's first `returnTo`; keep it allowlisted per route rather than generically
  sanitized (open-redirect class, `return-to.test.ts` pins the rejections).
- Error copy is keyed off backend codes (`openid4vp-invocation.ts`), and the five audit
  actions + target are mirrored in `audit-event.ts` and both locales (the parity test in
  `audit-event.test.ts` enforces it).

## 8. Testing

**Locally, end to end**, with the Compose stack and the dev verifier:

1. Hold a real credential: `ATTESTATION_HOLDER=irmago` (+ master key) in `.env`, and have an
   org receive a credential through the existing attestation flow (issue from another org
   over QERDS, accept the offer) — the stub holder cannot present anything real.
2. `COMPOSE_PROFILES=verifier` in `.env` (or `docker compose --profile verifier up
   devverifier` beside `npm run dev`). Open http://localhost:8091, enter the held
   credential's vct and the claims to ask for, pick `direct_post` or `direct_post.jwt`.
3. Follow the "Share with the business wallet" link: it lands on `/openid4vp?…`, redirects
   to login if needed (scan with your Yivi wallet), shows the org picker naming
   "Dev Verifier", and after picking, the verifier page shows the verified disclosure —
   issuer chain (staging + production Yivi anchors, plus `DEVVERIFIER_ISSUER_TRUST_CHAIN`),
   selective disclosures, KB-JWT signature against `cnf`, nonce and audience.

Bench-verified 2026-09-10 on the Compose stack with `compose.wsca.yaml` layered: the `yivi`
org received a KVK registration from the Veramo staging issuer over the local Domibus, and
presented `legalName` alone to the dev verifier in both response modes, KB-JWT signed by the
staging wallet-provider WSCA. Two things bit on the way and are listed under §9.

The wallet backend fetches `request_uri` and posts the response at
`http://devverifier:8090` (container DNS, allowed by the insecure-http flag); the browser
uses `localhost:8091`. The dev chain is checked in under `dev-setup/devverifier/` (SAN
`devverifier`, root inlined in `compose.override.yaml`); without files `cmd/devverifier`
mints an ephemeral identity and prints the root to paste into
`OPENID4VP_VERIFIER_TRUST_CHAIN`.

**On staging.** The Delivery workflow builds and mirrors a `devverifier` image
(`backend/docker/devverifier/Dockerfile`, context `backend/`, no `wsca` tag, 10 MB, runs as
`nobody`, `/healthz` for probes) alongside the backend and sidecar. `yivi-businesswallet-ops`
deploys it under `devverifier_deploy` with an ingress on `devverifier_host`
(`business-wallet-verifier.staging.yivi.app`); its CA and relying-party certificate are
minted by Terraform's `tls` provider and the backend trusts that CA through
`OPENID4VP_VERIFIER_TRUST_CHAIN`, with `OPENID4VP_PRESENTER_AUTO_PRESENT` on for staging.
The backend reaches the verifier over the public ingress (https, public address), so no
dev flag is needed there. `cmd/devverifier` loads the identity from
`DEVVERIFIER_CHAIN_FILE` / `DEVVERIFIER_KEY_FILE` (PKCS#8).

**Automated:**

- Unit (`openid4vppresenter/*_test.go`): verifying validator against a generated chain
  (trusted, wrong SAN, untrusted root, tampered, encrypted mode with/without keys),
  structural decoder matrix, URL policy and dial-time private-network block, redirect /
  size / status refusal, URL-stripping, JWE encryption that only the verifier's key opens,
  start-form rejections, metadata derivation.
- Integration (`eudiholder/engine_present_integration_test.go`): a holder key seeded the
  way the receive flow stores it, an SD-JWT VC bound to it with two SD claims, and
  `Present` asked for one — asserts exactly that disclosure travels and the KB-JWT verifies
  with the holder key over the right `sd_hash`, nonce and audience; plus
  `ErrNoMatchingCredential` for an unknown type and an empty org.
- Integration (`internal/integration/openid4vp_presenter_test.go`): full HTTP flow with
  the **verifying** validator and a fake relying party signing real JARs: pre-auth
  start/status, 401 on orgs, membership-only picker, 403 for a non-member org, delivered
  `vp_token` + `state`, 409 on reuse, audit trail without query/response material, user
  binding, rejection matrix with no rows persisted, well-known on the root mux.
- Frontend: `return-to.test.ts`, `openid4vp-invocation.test.ts`.

## 9. Open

- **#113**: replace `OPENID4VP_PRESENTER_AUTO_PRESENT` with the consent layer's decision at
  `org_selected`; the `PresentationDenied` audit action is already there for its refusals.
- Presenting to the hosted Yivi verifier needs a relying-party certificate that authorizes
  organization credential types; today its certificate lists `pbdf-staging.*` only.
- A `wallet_nonce`-driven `post` fetch (sending `wallet_metadata`) if a verifier ever
  requires it; today `post` falls back to GET as the spec allows.
- **wallet-provider bug, worked around here:** `irmabinding.Signer.Reference` matches the
  cnf key against the WSCA key list by parsing `public_key_hex` as DER, but that field is the
  raw EC point (`public_key_der_hex` is the DER form), so it never matches. `wscaRowSigner`
  (`engine_wsca_binder_on.go`) resolves the WSCA key id from the holder-key row the issuance
  binder wrote (`wsca:<key_id>` in the private-key column, keyed by DID URL or thumbprint) and
  only falls back to the list. Fix belongs in `wallet-provider`; the workaround can stay, it
  is one round trip fewer.
- irmago's verifier-side KB-JWT check accepts only `cnf.jwk`; the Veramo issuer binds via
  `cnf.kid` = `did:key`. The dev verifier checks the KB-JWT itself for both forms
  (`devverifier.verifyKeyBinding`); a real verifier built on irmago hits the same TODO.
