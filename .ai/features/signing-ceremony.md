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

See also `.ai/features/qtsp-signing-demo.md` (the hosted QTSP + `--profile signer`, incl. the
authorization_server) and `internal/csc` (per-org provider settings).
