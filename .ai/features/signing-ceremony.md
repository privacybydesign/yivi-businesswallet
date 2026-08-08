# Qualified-signing ceremony (RP-centric SCA over CSC + OID4VP)

Status: **implemented** (backend + gated frontend). Builds on the CSC provider settings
(`internal/csc`, `.ai/features/qtsp-signing-demo.md`). The business wallet acts as the
**RP-centric Signature Creation Application (SCA)** driving a remote QTSP's CSC API v2 to
create a qualified electronic signature over a PDF (PAdES).

## Who does what

The QTSP reference implementation is **backend-only** and owns the wallet ceremony: on
`/oauth2/authorize` its authorization_server starts an **OID4VP** presentation at a verifier
and renders the QR the wallet scans. We are the **client**: we compute the document hash,
launch the browser at the QTSP's authorize URL, catch the redirect back, exchange the code,
call `signHash`, and assemble the signed PDF. We never talk to the verifier directly and the
QTSP never sees the document — only its hash.

## The two-ceremony model (a hard constraint, not a choice)

PAdES SignedAttributes embed a hash of the **signing certificate** (ESS `signingCertificate`),
so the document hash depends on the cert — which must therefore be known **before** the
hash-bound `authorize`. But fetching the cert (`credentials/info`) needs a token, which needs
a ceremony. So signing is split:

1. **Link credential** (once, `POST /orgs/{slug}/signing/credential/link`): a `scope=service`
   ceremony → `credentials/list` (auto-issues if none) → `credentials/info` → cache the
   credential id + certificate + chain per (org, user) in `signing_credentials`.
2. **Sign a document** (per doc, `POST /orgs/{slug}/signing/requests`): prepare the PDF against
   the cached cert to get the hash → `scope=credential` `authorize` binding that hash (SCAL2)
   → token → `signHash` → embed. One wallet scan per document.

## SCAL2 = the OAuth authorize (no separate `credentials/authorize`)

This reference impl folds the SCAL2 credential authorization into `/oauth2/authorize`
(`scope=credential` + `hashes`/`hashAlgorithmOID`/`credentialID`/`numSignatures`, **PKCE
S256 mandatory**). The resulting access-token **JWT is the SAD** — `signHash` re-validates the
request against its claims. There is no `credentials/authorize` call and no separate SAD.

## PAdES: single parked-goroutine pass (`internal/signing/pades.go`)

The signature is produced remotely *after* a minutes-long wallet ceremony, so it cannot be a
synchronous `crypto.Signer.Sign` callback. Instead `StartSign` runs **one** `digitorus/pdfsign`
pass in a goroutine whose `crypto.Signer.Sign` **publishes the ByteRange digest** (which we bind
into `authorize`) and then **blocks** until the QTSP signature arrives via the callback, at
which point the pass completes and embeds it. Because there is exactly one pdfsign pass, `pkcs7`
stamps the CMS `signingTime` once — so **no library patch/vendor is needed** (an early
alternative, a deterministic-time double-run, would have required patching `pkcs7`). Proven by
`TestPAdESExternalSigningProducesVerifiablePDF` (`ValidSignature=true`). Scope: **PAdES-B/B-T**;
B-LT/B-LTA (validation data, archival timestamp) need a separate DSS augmentation seam.

## Packages & routes

- `internal/signingprovider` (leaf): CSC-v2 + OAuth2 client + a `StubProvider` (local EC key +
  self-signed cert) that makes the whole ceremony testable with no network/wallet. Errors are
  redaction-safe (status code only; never URL/token/body).
- `internal/signing` (org slice): the link + sign flows, an **in-memory session manager**
  (parked goroutine + prepared PDF, bounded by `SessionTTL`), the store (`signing_credentials`,
  `signing_requests`), and the handlers.
- Routes: `GET …/signing/availability` (member-safe: CSC configured&&enabled), `GET …/signing/credential`,
  `POST …/signing/credential/link`, `POST …/signing/requests`, `GET …/signing/requests/{id}`,
  `GET …/signing/requests/{id}/document`; plus the **central** `GET /api/v1/signing/callback`
  (the fixed OAuth redirect target, correlated by an unguessable `state`, no session).
- Audit: `signing.credential_linked` / `.requested` / `.completed` / `.failed` — kept **out** of
  the notifications catalogue (metadata is document-related).
- Frontend: the `/{slug}/signing` page (link + upload → authorize QR handoff → poll → download),
  surfaced as a "Sign documents" sidebar plugin gated on `signing/availability`.

## Constraints & limitations (accepted for this capability)

- **Wallet/verifier:** the QTSP authorize step requests an **mdoc PID** (`eu.europa.ec.eudi.pid.1`)
  via the **public EUDI verifier** (`verifier-backend.eudiw.dev`) — it does **not** reuse our
  Yivi/SD-JWT login wallet. Exercising the ceremony end to end needs an **EUDI reference wallet
  with a dev PID** and the `--profile signer` stack running.
- **In-memory sessions:** an in-flight signing session (goroutine + prepared PDF) lives in memory
  — it does not survive a backend restart and assumes a single API instance. Fine for a demo;
  a durable/multi-instance version would persist the prepared state and reconcile the callback.
- **`DefaultRedirectURI`** is a dev constant (`http://localhost:8080/api/v1/signing/callback`,
  matching the AS client registration); a real deployment behind another host must make it
  configurable.
- Sign algorithm is ECDSA-P256 / SHA-256 only (the reference QTSP's supported set).

## Running it locally (verified end to end against a real EUDI wallet)

1. `docker compose --profile signer up --build` — brings up the app plus the QTSP resource
   server (`:8085`) and authorization server (`:8084`). First run only: if `qtsp-authz` fails
   with "table doesn't exist", `docker compose --profile signer down -v` once and up again (the
   Spring OAuth schema loads only on a fresh `signer-mysql` volume).
2. `.env` needs `CSC_ENCRYPTION_KEY` (`openssl rand -hex 32`) or the org can't save the client
   secret.
3. In **Settings → Signing**, use the sample preset and set:
   - **base URL `http://qtsp-signer:8085`** (NOT `localhost:8085` — the backend calls it
     server-side from its container),
   - client id `yivi-business-wallet`, client secret `yivi-business-wallet-demo-secret`, enable.
4. Install the **EUDI reference wallet**, get a dev **PID**. Add the EUDI verifier's RP trust
   anchor to the wallet if prompted (`certificate_of_issuers/PIDIssuerCA02-EU.cacert.pem` from
   the reference repo). **Yivi does not work here** — it rejects that CA's mdoc EKU and lacks an
   mdoc PID.
5. **Sign documents** page → link the credential (one scan) → upload a PDF → scan → download the
   signed PDF.

### Local-Docker gotchas baked into the compose (each was a real failure while bringing this up)

- **CSC base URL** must be the container name `qtsp-signer:8085`, not `localhost` (backend calls
  it server-side).
- **`VERIFIER-DOMAIN` / `WALLET-SCHEME`** are passed to `qtsp-authz` as **Spring command-line
  args**, not `environment:` — Docker Compose silently drops env var names containing hyphens.
- **OAuth issuer host split**: the browser reaches the AS at `localhost:8084` (the authorize
  URL) but the backend reaches it at `qtsp-authz:8084` (the server-side token exchange), so
  `SIGNING_OAUTH_ISSUER_INTERNAL` overrides only the backend's token-exchange host. Empty in
  production, where the QTSP is one URL for both.
- **`client_id` in the token body**: the reference token endpoint requires `client_id` as a form
  field even with `client_secret_basic`; `signingprovider.ExchangeToken` sends it in both.
- **Authorize `hashes` are base64url**, `signHash` hashes are standard base64 (the token claim
  the AS re-encodes to). Same digest, two encodings — mixing them 500s the AS.

## Verifying a signed PDF

The produced PDF is a detached CMS/PAdES signature (`/Type /Sig`, `/ByteRange`,
`/SubFilter /adbe.pkcs7.detached`). To check one:

- **`pdfsig file.pdf`** (poppler): shows the signer (the PID identity), signing time, SHA-256,
  and "Signature is Valid".
- **Adobe Acrobat** Signatures panel — valid signature, but "signer's identity is unknown".
- **`digitorus/pdfsign/verify`** — the programmatic check `pades_test.go` uses.

**Expected: "certificate issuer is unknown" / "not trusted".** The demo QTSP self-signs the cert
(the EJBCA-free path), so it is not a qualified cert in the EU Trusted Lists / Adobe AATL — this
is the "plumbing, not qualified compliance" line made concrete. A real production QTSP issues a
cert that chains to an EU trust list, and the same signature would validate as trusted/qualified.

### Follow-ups
- The signature subfilter is `adbe.pkcs7.detached` (a widely-accepted CMS detached signature).
  For strict PAdES-baseline badging, switch pdfsign to the `ETSI.CAdES.detached` subfilter.
- `DefaultRedirectURI` and the local-Docker host overrides above are dev conveniences; a real
  deployment points at one publicly-reachable QTSP URL and needs none of them.

See also `.ai/features/qtsp-signing-demo.md` (the hosted QTSP + `--profile signer`, incl. the
authorization_server) and `internal/csc` (per-org provider settings).

## Co-signing (multiple signers + recipient delivery)

The single-signer ceremony above is the building block; a **request** is now an org-level
co-signing object.

- **Model.** `signing_requests` carries `created_by`, the `original_document`, an accumulating
  `signed_document`, a `signing_mode` (`parallel`/`sequential`), a recipient
  (`recipient_channel` none/qerds/email + address/name/message) and a `delivery_status`.
  `signing_request_signers` holds one row per selected member (`sign_order`, per-signer `status`).
  Each member signs with **their own** linked credential — the PK on `signing_credentials` is
  still `(org, user)` — so the finished PDF carries **N qualified signatures**, applied
  incrementally (`pades_multisig_test.go` proves two incremental signatures both verify).
- **Order.** *Parallel* = any order; *sequential* = ascending `sign_order`, and a signer's turn
  is only offered once every lower-order signer has signed (`ListPendingForUser` /
  `Service.checkTurn`). **"Parallel" is not simultaneous:** incremental PAdES appends to a specific
  base document and cannot merge concurrent signatures, so an in-memory **per-request in-flight
  lock** (`Service.acquire`/`release`) serialises the actual signing passes either way.
- **Flow.** `POST /requests` (member) creates the request (validates signers against the member
  directory, stores the PDF); `POST /requests/{id}/sign` (member, must be a pending signer whose
  turn it is) runs *that signer's* ceremony over `GetLatestDocument` (signed ?? original);
  `finishSign` writes the new bytes + marks the signer signed, and when all have signed marks the
  request completed and **delivers** it.
- **Delivery.** On completion the finished PDF goes to the recipient over the chosen channel:
  `email` reuses `email.Service.SendSignedDocument` (a new `signed_document` mail Kind + the new
  `mailer` attachment support — `multipart/mixed`); `qerds` reuses `qerds.Service.Send` with the
  PDF as an attachment from the org's default QERDS address. Delivery is best-effort — a failure
  leaves the request `completed` with `delivery_status=failed`, surfaced in the history, not fatal.
  The `signing` slice stays decoupled via consumer interfaces (`memberDirectory`,
  `documentDeliverer`) implemented by adapters in `cmd/api` (`signing_adapters.go`).
- **UI.** The Sign page is tabbed: **To sign** (documents awaiting me → run my ceremony),
  **New request** (upload + member multi-select + order + recipient), **My credential** (the
  once-off link, moved out of the per-document flow). A separate admin **Signed documents** route
  (next to Audit log) lists the org's requests, cursor-paginated, with per-signer + delivery status.
- **Audit.** Adds `signing.signed` (a signer signed) and `signing.delivered` (delivered to the
  recipient) alongside the existing `signing.requested`/`.completed`/`.failed`.
- **Not in this iteration:** signer notifications are **in-app only** (the "To sign" tab) — no
  per-signer notification email Kind yet; the active ceremony is still in-memory/single-instance
  (intermediate document state is now persisted between signers).
