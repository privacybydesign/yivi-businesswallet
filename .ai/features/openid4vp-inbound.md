# Feature: Inbound OpenID4VP — the business wallet as holder/presenter

**Status:** Implemented for the invocation and routing seam (issue
[#188](https://github.com/privacybydesign/yivi-businesswallet/issues/188), the first slice
of the OpenID4VP-holder epic [#112](https://github.com/privacybydesign/yivi-businesswallet/issues/112)).
The presentation cryptography (#112) and the consent/approval policy
([#113](https://github.com/privacybydesign/yivi-businesswallet/issues/113)) are seams here,
not implementations. Design of record: `.ai/plans/openid4vp-invocation.md`.
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
| `Present` seam on the holder engine | `eudiholder.Holder.Present`, `eudiholder.Presentation`, `eudiholder.Formats()` |
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

## 3. Request Object validation is a seam, not yet cryptography

`openid4vppresenter.Validator` is what the service persists through. Two implementations:

- `RefusingValidator` — **the default.** Every inbound request fails 501
  `request_object_validation_unavailable` before a row is written. A hosted deployment
  stays here until #112 supplies chain validation.
- `UnverifiedDecoder` — dev / CI, behind `OPENID4VP_PRESENTER_ALLOW_UNVERIFIED_REQUEST_OBJECTS=true`.
  Structural only: compact JWS, `alg` not `none`, non-empty signature, payload `client_id`
  equal to the invoking one, `x509_san_dns:` prefix (identity = the DNS name),
  `response_type=vp_token`, `response_mode=direct_post` (`direct_post.jwt` needs JARM
  encryption → #112), `response_uri` under the URL policy, URL-safe nonce, non-empty DCQL,
  `exp` not passed. **It does not verify the signature or the certificate.**

`Validator.ClientIDPrefixes()` feeds `client_id_prefixes_supported` in the metadata, so the
refusing default advertises none rather than asserting `x509_san_dns`.

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
CI) skips the wait: `Holder.Present(orgID, dcql, nonce, audience=client_id)` → form POST
`vp_token` + `state` to `response_uri` → `completed`, returning the verifier's `redirect_uri`
(validated absolute http(s)) for the "return to verifier" button. Any failure consumes the
row as `denied` with the failing step as reason — one nonce, one attempt.

`Holder.Present`: the irmago `Engine` returns `ErrPresentNotImplemented` (#112 owns DCQL
matching, disclosure selection, KB-JWT signing through the software/WSCA binder); the
`StubHolder` returns one placeholder entry per DCQL credential-query id so the whole loop runs
offline. `eudiholder.Formats()` reads the algorithm from the JOSE library irmago signs with,
which is what `vp_formats_supported` publishes.

## 6. Config

| Variable | Default | Meaning |
|---|---|---|
| `OPENID4VP_TRANSACTION_TTL` | `5m` | Invocation → response; shorter than the outbound 15 m on purpose |
| `OPENID4VP_PRESENTER_ALLOW_INSECURE_HTTP` | `false` | Dev: http + private-network verifier URLs |
| `OPENID4VP_PRESENTER_ALLOW_UNVERIFIED_REQUEST_OBJECTS` | `false` | Dev: `UnverifiedDecoder` instead of `RefusingValidator` |
| `OPENID4VP_PRESENTER_AUTO_PRESENT` | `false` | Dev: complete right after selection (stands in for #113) |

`compose.override.yaml` sets all three flags to `true` for the dev stack; `.env.example`
documents them as never-on-hosted. `main.go` logs a warning at boot for each one that is on.

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

- Unit: `openid4vppresenter/*_test.go` — validator accept/reject matrix, URL policy,
  private-network dial block (TLS test server), redirect/size/status refusal, URL-stripping,
  start-form rejections, metadata derivation.
- Integration (`internal/integration/openid4vp_presenter_test.go`): full HTTP flow against a
  fake verifier (request object host + direct_post receiver): pre-auth start/status, 401 on
  orgs, membership-only picker, 403 for a non-member org via the tenant seam, delivered
  `vp_token` + `state`, 409 on reuse, audit trail without query/response material, user
  binding, rejection matrix with no rows persisted, well-known on the root mux.
- Frontend: `return-to.test.ts`, `openid4vp-invocation.test.ts`.

## 9. Open for #112 / #113

- Signature + `x509_san_dns` chain validation behind `Validator`; `direct_post.jwt`.
- `Engine.Present`: DCQL evaluation, disclosure selection, KB-JWT via the configured binder.
- Replace `OPENID4VP_PRESENTER_AUTO_PRESENT` with the consent layer's decision at
  `org_selected`; the `PresentationDenied` audit action is already there for its refusals.
- A `wallet_nonce`-driven `post` fetch (sending `wallet_metadata`) if a verifier ever
  requires it; today `post` falls back to GET as the spec allows.
