# Feature: Verification templates and checks (business wallet as requester)

**Status:** Implemented, requester side of issue
[#245](https://github.com/privacybydesign/yivi-businesswallet/issues/245): an
organisation defines what it wants to see (a template), starts a presentation
request for it, shows the link as a QR, polls for the disclosure and grades it
against its own issuance ledger. The scenario is Gemeente Nijmegen checking an
APV standplaatsvergunning at a market stall. The holder side of the same flow
is the existing inbound entry (`.ai/features/openid4vp-inbound.md`): the QR
opens `/openid4vp?client_id=…&request_uri=…` in the holder's business wallet.
**Counterpart:** `.ai/features/auth-openid4vp.md` is the same requestor seam
used for a natural person's login disclosure.

---

## 1. What exists

| Piece | Where |
|---|---|
| Domain slice (handler → service → store, ledger seam) | `backend/internal/verification/` |
| Tables | `verification_templates`, `verification_sessions` (migrations `20260929200000`, `20260929200100`) |
| Runtime-built single-credential query at the hosted verifier | `openid4vpverifier.Client.StartQuery`, `openid4vpverifier.Query`, `Presentation.QueryClaims` / `QueryExpiresAt` |
| Ledger lookup by disclosed claims | `attestation.Store.FindIssuedByClaims` (`attributes @> claims`, newest row) |
| Audit vocabulary | `audit.VerificationTemplateCreated/Deleted`, `audit.VerificationStarted/Completed`, targets `verification_template`, `verification_session` |
| Seed | `seedNijmegenVerification` (dev seed and `seed -partners`): one template over `nl.nijmegen.apv.standplaatsvergunning` |
| Screen | `/:orgSlug/verifications` (`frontend/src/routes/verifications.tsx`), nav item "Checks" / "Controles" |

## 2. Flow

```
 handhaver (Nijmegen wallet)         backend                          hosted verifier      holder (Groentekraam wallet)
   │ pick template, Start ─────▶ POST /orgs/{slug}/verifications
   │                              template → Query{vct, claims} ──▶ POST /ui/presentations
   │                              verification_sessions(pending)  ◀── transaction_id, client_id, request_uri
   │◀── {id, walletLink, browserLink} (audit verification.started)
   │ QR = browserLink ─────────────────────────────────────────────────────────────────▶ GET /openid4vp?client_id&request_uri
   │ poll GET …/verifications/{id} ──▶ GET /ui/presentations/{tx} (pending)             login, pick org, approve → direct_post
   │                              ◀── vp_token ─────────────────────── ◀────────────────────────────┘
   │                              grade: verified, not_expired, issued_here, not_revoked
   │◀── completed + claims + checks (audit verification.completed)
```

- **Two link forms.** `walletLink` is the verifier's `openid4vp://` deeplink;
  `browserLink` is the same query on `APP_BASE_URL/openid4vp`. The QR carries
  the https form because a phone camera hands a custom scheme to the personal
  Yivi app, while the https URL opens the holder's business wallet in the
  browser (§7 of the inbound doc). A holder on another deployment uses the
  wallet link with its own scanner.
- **The verifier's transaction id never leaves the backend**; the browser polls
  by session id, as the login flow does. `wallet_link` is stored so a reloaded
  page redraws the QR.
- **Expiry is derived on read** (`Session.EffectiveStatus`, TTL
  `PRESENTATION_SESSION_TTL`); no pruner, the rows are the check history.
- **Grading** happens on the first poll that finds the disclosure and is
  stored with the session: `verified` (the verifier returned something under
  the query credential id; it verified signature, chain and key binding),
  `not_expired` (issuer `exp`, else the ledger's `expires_at`), `issued_here`
  (a ledger row of the same vct whose attributes contain every disclosed
  claim) and `not_revoked` (that row is `claimed`; detail names the status
  otherwise, or `no_ledger_entry`). The audit event carries the verdict and
  the failed check names, never the claims.

## 3. Permissions

Templates are admin-managed (`RequireOrgAdmin`), readable by members; starting
and reading a check needs membership. The issue's `verifications:run`
permission waits for the RBAC layer, which is not in `main` (see
`.ai/conventions/BACKEND.md`); this follows `internal/organization/mandate.go`'s
precedent.

## 4. Open

- Presenting to the hosted Yivi verifier needs a relying-party certificate
  that authorises organisation credential types (`openid4vp-inbound.md` §9);
  its certificate lists `pbdf-staging.*` only, so the holder's
  `VerifyingValidator` refuses the request until ops registers one. The
  Compose dev stack with unverified request objects, or the dev verifier, is
  the bench meanwhile.
- The holder's in-app scanner and paste field, the `/present?uri=` deep link,
  the `attestations:present` permission and the market/standplaats comparison
  (#245 remaining items) are not built here.
- Template update (PATCH) is not built; delete and recreate.

## 5. Testing

- Unit: `openid4vpverifier/query_test.go` (DCQL shape of `StartQuery`,
  `QueryClaims`, `QueryExpiresAt`); `verification/service_test.go` (start,
  links, pending, every grading outcome, session expiry, org scoping).
- Integration: `internal/integration/verification_test.go` runs the HTTP flow
  on the assembled router against a real Postgres with the fake verifier: a
  claimed permit grades valid, a revoked one invalid with the ledger status
  named, history and audit rows, and the admin/member/outsider gates.
- Frontend: `lib/verification.test.ts`; `audit-event.test.ts` pins the new
  audit copy.
