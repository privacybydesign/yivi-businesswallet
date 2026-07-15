# Feature: OpenID4VP / EUDI login (replacing IRMA/Yivi disclosure)

**Status:** Implemented (backend + frontend). Login discloses passport/id-card +
email + phone via the hosted EUDI verifier; IRMA/`irmago` removed. Frontend renders
the OpenID4VP QR via `qrcode` and polls the backend session by id.
**Supersedes:** the IRMA/Yivi disclosure login (`irmarequestor`, `auth/disclosure.go`, the
`yivi.newWeb` frontend widget). This is a **protocol swap**, not a new auth model — the
user/session/cookie/invite logic below the seam is unchanged.
**Enables:** the "PID" seam that `.ai/features/wallet-bootstrap.md` depends on.
**Reference integration:** `/Users/dibranmulder/code/openid4vp-demo-frontend` (working
OpenID4VP + EUDI verifier demo).

---

## 1. Summary

Replace IRMA/Yivi disclosure with **OpenID4VP** presentations verified by the **EUDI
reference Verifier Endpoint**. Login discloses **passport OR id-card** (verified identity)
plus **email** and **phone (mobile number)**. This gives us, at login, a real EUDI-native
identity credential — the "PID" the wallet bootstrap needs to present to KVK — instead of
IRMA's email-only disclosure.

The account key stays **email** (matches the existing `users.FindByEmail` model); the
passport/id-card supplies verified **name** (so name is captured at login, not only at
invite-accept); **phone** is captured as a contactable identifier.

---

## 2. Architecture — requestor in front of the hosted EUDI verifier

We do **not** implement the OpenID4VP verifier role (JAR signing, `request_uri` hosting,
`vp_token` crypto). We front the **hosted EUDI reference verifier** (Yivi's deployment,
`verifierapi.openid4vc.staging.yivi.app`) as a thin requestor/orchestrator — the same
stance the backend already takes toward the IRMA daemon and QERDS providers ("requestor,
not the authentic source / QTSP"). Self-hosting the verifier is a later config swap, not a
rewrite.

```
 phone/EUDI wallet ──scan/deeplink──▶ (openid4vp:// → open.yivi.app universal link)
        ▲                                        │ presents passport/idcard + email + phone
        │ QR/deeplink                            ▼
 ┌──────┴───────┐   POST /auth/session   ┌──────────────────┐  POST /ui/presentations
 │  frontend    │───────────────────────▶│  auth.Service    │─────────────────────────▶ EUDI
 │ (React)      │   GET  /auth/session/id │ openid4vpverifier│  GET /ui/presentations/{txid}  verifier
 └──────────────┘◀───────────────────────└──────────────────┘◀───────────────────────── (hosted)
     opaque session id, walletLink            vp_token (SD-JWT VC) → DisclosedIdentity
```

The backend maps our own opaque **session id** ↔ the verifier's **`transaction_id`** and
polls the verifier server-side. The frontend never sees the `transaction_id` (the EUDI
poll endpoint is unauthenticated — exposing it would let anyone poll a presentation).

---

## 3. The verifier client seam — `internal/openid4vpverifier`

New package replacing `internal/irmarequestor`. Two calls, mirroring the demo
(`openid4vp-demo-frontend/src/verifiers.ts:315-342`):

- `StartPresentation(ctx, req PresentationRequest) (Session, error)` →
  `POST {EUDI_VERIFIER_URL}/ui/presentations` with the envelope:
  ```jsonc
  { "type": "vp_token", "dcql_query": <DCQL>, "nonce": <random>,
    "jar_mode": "by_reference", "request_uri_method": "post",
    "issuer_chain": <trusted-issuer CA PEM> }
  ```
  Returns `{ transactionId, walletParams }`; `walletLink = openid4vp://?<urlencode(walletParams)>`.
  **Generate a real random `nonce`** (the demo hardcodes `"nonce"` — must NOT copy).
- `Result(ctx, transactionId) (Presentation, error)` →
  `GET {EUDI_VERIFIER_URL}/ui/presentations/{transactionId}`; non-200 = pending; 200 =
  `{ vp_token: { <credId>: [ <sd-jwt-vc>, ... ] } }`.
- `Ping(ctx)` boot probe → "can we create a presentation request?" (create + discard a
  login request), replacing the IRMA start-then-cancel probe. Fatal on failure, same as today.

The `RequestAuthenticator` seam carries over conceptually: today it auths to the IRMA
daemon; here it auths to the verifier backend if the deployment requires it (the demo's
EUDI calls are unauthenticated; self-hosted may differ).

### Credential requests are DCQL (not IRMA condiscon, not Presentation Exchange)

Format `dc+sd-jwt`, identified by `vct_values`, claims by `path`. From the demo's presets
(`verifiers.ts:34-307`), the schemes we use (staging):

| Purpose | vct | claims |
|---|---|---|
| Identity (passport) | `pbdf-staging.pbdf.passport` | firstName, lastName, dateOfBirth, nationality, gender, documentNumber, dateOfExpiry |
| Identity (id-card) | `pbdf-staging.pbdf.idcard` | same set as passport |
| Email (account key) | `pbdf-staging.sidn-pbdf.email` | email, domain |
| Phone | `pbdf-staging.sidn-pbdf.mobilenumber` | mobilenumber |

**Login DCQL** = one identity credential (passport **OR** id-card, via
`credential_sets.options`, demo `verifiers.ts:139`) **AND** email **AND** mobilenumber:
```jsonc
{ "credentials": [
    {"id":"passport","format":"dc+sd-jwt","meta":{"vct_values":["pbdf-staging.pbdf.passport"]},
     "claims":[{"path":["firstName"]},{"path":["lastName"]},{"path":["dateOfBirth"]},{"path":["nationality"]}]},
    {"id":"idcard","format":"dc+sd-jwt","meta":{"vct_values":["pbdf-staging.pbdf.idcard"]},
     "claims":[{"path":["firstName"]},{"path":["lastName"]},{"path":["dateOfBirth"]},{"path":["nationality"]}]},
    {"id":"email","format":"dc+sd-jwt","meta":{"vct_values":["pbdf-staging.sidn-pbdf.email"]},"claims":[{"path":["email"]}]},
    {"id":"phone","format":"dc+sd-jwt","meta":{"vct_values":["pbdf-staging.sidn-pbdf.mobilenumber"]},"claims":[{"path":["mobilenumber"]}]} ],
  "credential_sets": [
    {"options":[["passport"],["idcard"]]}, {"options":[["email"]]}, {"options":[["phone"]]} ] }
```
DCQL definitions live in config/constants (no magic strings), one per request purpose
(login; and, for the wallet bootstrap, the identity-for-KVK request).

---

## 4. Result parsing — rewrite of `auth/disclosure.go`

The current `disclosure.go` is entirely IRMA-result-shaped (`res.Disclosed[N][M]`,
`ProofStatus`, `AttributeProofStatusPresent`, `RawValue`). Replace with SD-JWT VC parsing:
split the compact token on `~`, drop the issuer JWT (first) and KB-JWT (last), base64url-
decode each middle disclosure `[salt, claimName, claimValue]` (demo `verifiers.ts:6-14`),
collect claims per credential id, and build the existing protocol-neutral
`auth.DisclosedIdentity` — extended to `{ Email, Name identity.Name, Phone, Document }`
where `Document = { number, dateOfBirth, nationality }` from the passport/id-card.

**Trust boundary (important):** the hosted EUDI verifier performs signature / KB-JWT /
`issuer_chain` verification; our backend decodes disclosures for use. If we self-host the
verifier it still does the crypto. We must **not** treat client-side disclosure decoding as
verification (the demo explicitly does not verify — `verifiers.ts` §5 caveat). Keep the
`issuer_chain` CA in config so the request pins the trusted issuer.

`internal/identity` (`Reconcile`, `Name`, MRZ folding) is **unchanged** — it already has no
IRMA imports and now simply receives the name from the passport/id-card claims.

---

## 5. Swap surface (what changes vs. what stays)

**Replace / rewrite:**
- `internal/irmarequestor/` → `internal/openid4vpverifier/` (§3).
- `auth/disclosure.go` → SD-JWT VC parsing (§4).
- Leaked IRMA types in public signatures → protocol-neutral: `irma.RequestorToken` →
  opaque `SessionID string`; drop `irmaserver.SessionPackage`/`SessionResult` and
  `irma.AttributeTypeIdentifier` from `auth/service.go`, `auth/handler.go`,
  `organization/service.go`, `organization/handler.go`. The handler's
  `irma.RequestorToken(r.PathValue("token"))` becomes a plain string session id.
- `internal/config`: drop `IRMA_*`; add `EUDI_VERIFIER_URL`, the issuer-chain CA PEM path,
  and the DCQL request definitions selector. `IRMA_CLIENT_URL` (wallet-facing) → the
  universal-link host (`open.yivi.app`) for the deeplink rewrite.
- `cmd/api/main.go:102-122`: attribute-identifier construction + IRMA `Ping` → build the
  verifier client + the new presentation-request `Ping` probe.
- Frontend: remove `@privacybydesign/yivi-frontend` / `yivi-css`; replace the `yivi.newWeb`
  blocks (`login.tsx:59-71`, `invite-accept.tsx`, `ui/identity-disclosure.tsx`) with the
  demo's pattern — QR/deeplink of `walletLink` (`QrCodeComponent`, `walletLink.ts` scheme↔
  universal-link rewrite) + a 500ms poll (`App.tsx:192-204`) on **our** `/auth/session/{id}`
  endpoint, then `POST /claim`. The `DisclosureContent`-style result maps onto the existing
  downstream UI.

**Keep (clean seam boundaries — already protocol-neutral):**
- `internal/identity`, `auth/issuer.go`, `auth/cookie.go`, `auth/middleware.go`,
  `auth/context.go`, `auth/platformadmin.go`, `internal/session`, `internal/user`.
- The consumer interfaces `auth/service.go` `irmaRequestor` and `organization/service.go`
  `discloser` — keep their shape (rename the token type to `SessionID`), re-point at the new
  client, and `Authenticate` / `resolveUser` / `Reconcile` / invite-accept are unchanged.
- Frontend `api/http.ts`, `api/auth.ts` (except the claim contract), query hooks, route
  state machines.

---

## 6. Session / user model impact

- **Email stays the account key.** `users.FindByEmail` + `sessions.Mint` unchanged.
- **Name is now captured at login** (from passport/id-card), not only at invite-accept.
  This simplifies the org invite-accept identity path — the disclosed name is always
  present. `identity.Reconcile` still governs populate/upgrade/review.
- **Phone** stored on the user as a contactable identifier (new nullable column;
  edit-the-existing `create_users` migration per the pre-prod convention).
- The `Authenticate` idempotency key (currently `sha256(RequestorToken)`) becomes
  `sha256(SessionID)` — same shape.

---

## 7. Security posture

- **Random nonce per request**, server-side (never the demo's hardcoded `"nonce"`).
- **Never expose `transaction_id`** to the client — poll via our opaque session id.
- **Verification is the verifier's job**, not client-side disclosure decoding. Keep the
  trusted-issuer CA in config.
- Session cookie posture unchanged (`ybw_session`, HttpOnly, SameSite=Lax, host-scoped).

## 8. Open questions

1. **Users without a passport/id-card credential** — is email+phone alone a valid login for
   pre-existing/limited users, or is an identity credential mandatory? (Affects the DCQL
   `credential_sets` — identity `required:false` vs required.)
2. **Account linking** — if email changes issuer or a user has multiple identity
   credentials, how do we keep one `users` row? (Email key assumed stable.)
3. **Self-hosted vs hosted verifier** for production, and the `RequestAuthenticator` needed
   for a self-hosted deployment.
4. **mdoc/ISO-mDL** — the demo only has SD-JWT VC `pbdf-staging.*` schemes, no ISO-mdoc PID.
   If a real EUDI PID (mdoc) is required later, the parser needs an mdoc path.

## 9. Scope

**In:** migrate login + identity disclosure to OpenID4VP/EUDI (passport|id-card + email +
phone); the verifier client seam; frontend QR/deeplink/poll; disclosure parsing; boot probe.
**Out:** implementing the OpenID4VP *verifier* role ourselves; ISO-mdoc; self-hosting the
verifier; changing the session/user/invite/audit model.

## 10. Done when (if built)

- Login completes end-to-end against the EUDI verifier: passport|id-card + email + phone
  presented, `DisclosedIdentity` built, user found/created, session minted.
- No `irma`/`irmago` import remains in `backend/`; no `@privacybydesign/yivi-*` dep in
  `frontend/`.
- Nonce is random per request; the frontend never receives a `transaction_id`.
- Boot probe fails fast when the verifier can't accept a login presentation request.
- Backend + frontend verify sequences (`CLAUDE.md`) pass; integration test drives a faked
  verifier through the assembled router (mirroring the current faked-IRMA integration seam).

## Harvest

- Convention: note the verifier-seam pattern in `.ai/conventions/BACKEND.md` (replaces the
  irmarequestor note) on merge.
- Feature docs: this file; cross-linked from `.ai/features/wallet-bootstrap.md` (PID seam).
