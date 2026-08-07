# QTSP CSC-signing demo (`--profile signer`)

Self-hosted **QES-over-CSC** demonstration for issue
[#194](https://github.com/privacybydesign/yivi-businesswallet/issues/194). It hosts the EU
Digital Identity Wallet [wallet-driven / RP-centric QTSP reference
implementation](https://github.com/eu-digital-identity-wallet/eudi-srv-web-walletdriven-rpcentric-signer-qtsp-java)
so you can show how remote qualified signing over the Cloud Signature Consortium (CSC) API
v2 works — the same way `--profile domibus` shows how QERDS/AS4 works.

> **Demo, not a QTSP.** This proves the CSC signing *plumbing* on a **software HSM**
> (SoftHSM2) with self-signed certificates and authentication disabled. It is **not** a
> qualified trust service and produces **no** qualified signatures. Same posture as the
> Domibus bench: "proves plumbing, not qualified compliance."

## Run

```bash
docker compose --profile signer up --build
```

This starts (alongside the normal dev stack), all from one image (`QTSP_SERVER` picks the jar):

- `signer-mysql` — MySQL 8, shared by both servers; the Spring Authorization Server tables
  are loaded from `schema/` on first init.
- `qtsp-signer` — the CSC **resource server** (`http://localhost:8085`, SoftHSM inside).
- `qtsp-authz` — the OAuth2 + OID4VP **authorization server** (`http://localhost:8084`).

### Full ceremony (default: real auth)

By default the resource server validates the authorization server's JWTs, so `signHash`
requires the real RP-centric ceremony: `oauth2/authorize` (binds the document hashes,
scope `credential`) → the AS shows an **OID4VP QR** → wallet presents → `oauth2/token` →
`credentials/list`/`info` → `signHash`. The business-wallet backend (the SCA) drives this;
see `.ai/features/qtsp-signing-demo.md`.

> **The wallet step needs an EUDI reference wallet with a dev PID.** The AS requests an
> `mso_mdoc` PID from the **public EUDI verifier** (`verifier-backend.eudiw.dev`,
> `VERIFIER-DOMAIN`). This is a different credential and wallet than our Yivi/SD-JWT login —
> you cannot complete the scan with the Yivi wallet. To self-contain it fully you would also
> host the EUDI verifier-backend reference impl (not done here).

### Quick path (no wallet): `noauth`

To exercise `signHash` without the ceremony, run the resource server in `noauth` mode
(uncomment `SPRING_PROFILES_ACTIVE: noauth` on the `qtsp-signer` service). Then the
`/spike/seed-credential` + `/spike/health` endpoints are available and `run-demo.sh` does
the full seed → `signHash` → **verify with openssl** round-trip against `http://localhost:8085`
with no authorization server involved.

## What's in the image

The image builds upstream at a **pinned commit** (`UPSTREAM_REF` in the `Dockerfile`) and
applies `qtsp-demo.patch` — a `git diff` against that commit. The patch is small and every
change is marked `[SPIKE]`/`[DEMO]` at the call site. It does three things:

1. **SoftHSM-portable PKCS#11 mechanisms.** Upstream targets Utimaco's lenient PKCS#11;
   SoftHSM is stricter. Key wrap/unwrap uses `CKM_AES_KEY_WRAP_PAD` (RFC 5649) instead of
   `CKM_AES_CBC`-without-IV, and the certificate path pre-hashes then signs with raw
   `CKM_ECDSA` instead of the combined `CKM_ECDSA_SHA256` (which SoftHSM does not offer).
   `signHash` itself already used raw ECDSA. **A real vHSM/QSCD would not need this patch.**
2. **EJBCA-free self-sign** (`ejbca.enabled=false`): `CertificatesService` self-signs the
   credential's certificate (HSM-signing the TBS) instead of enrolling against a Certificate
   Authority, so no EJBCA is required for the demo.
3. **`noauth` header-stub auth**: under the `noauth` Spring profile the resource server reads
   authorization claims from `X-Stub-*` headers, so `signHash` can be driven without the
   OID4VP authorize flow. Plus a `noauth`-only `/spike/seed-credential` + `/spike/health`
   controller (a new file, `src/SpikeSeedController.java`).

The `docker-entrypoint.sh` initializes a SoftHSM token on first boot, resolves its assigned
slot id, and exports the `JACKNJI11_*` env the app reads.

## The authorization server (`qtsp-authz`)

`config/application-secret.yml` registers the SCA OAuth2 client (`yivi-business-wallet`,
`client_secret_basic`, `authorization_code`, `cross-device-flow` for the QR). The AS
persists it into `oauth2_registered_client` at startup, so the three Spring Authorization
Server tables must pre-exist — they are created from `schema/*.sql`, mounted into MySQL's
init dir (`JPA ddl-auto` does not create them). `SYMMETRIC_SECRET_KEY` is shared with the
resource server (the AS encrypts the identity claims the RS later decrypts), and the AS's
`SERVICE_URL_OAUTH2_ISSUER` must string-match the RS's `AUTHORIZATION_SERVER_ISSUER_URI`.

## What this demo does NOT cover

- **A self-hosted verifier.** The OID4VP step uses the *public* EUDI verifier; hosting the
  EUDI verifier-backend locally is out of scope.
- **Qualified anything.** Software HSM, self-signed certs, no EU trust list.

## Bumping the upstream pin

1. Update `UPSTREAM_REF` in the `Dockerfile`.
2. Regenerate `qtsp-demo.patch` against the new commit (clone, apply the three changes,
   `git diff > qtsp-demo.patch`) and re-verify with `run-demo.sh`.
3. Consider upstreaming the portability/build fixes — they make the reference impl run on
   any conformant token, not just Utimaco. See [`UPSTREAM.md`](./UPSTREAM.md) for the
   maintainer-facing write-up of each bug (symptom, root cause, location, fix).
