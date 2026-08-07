# QTSP CSC-signing demo (QES over CSC API v2)

Status: **Phase 1 implemented** (opt-in `--profile signer` demo). Issue
[#194](https://github.com/privacybydesign/yivi-businesswallet/issues/194).

## Why

We self-host a Domibus AS4 access point (`--profile domibus`) purely to demonstrate how
QERDS works. This is the symmetric move for **qualified electronic signatures**: self-host
the EU Digital Identity Wallet [wallet-driven / RP-centric QTSP reference
implementation](https://github.com/eu-digital-identity-wallet/eudi-srv-web-walletdriven-rpcentric-signer-qtsp-java)
so a visitor can see how remote qualified signing over the **Cloud Signature Consortium
(CSC) API v2** works end to end.

**We are not becoming a QTSP.** The business wallet's role in qualified signing is the
*requestor* (RP / Signature Creation Application) that drives a QTSP's CSC API — the same
external-provider relationship `internal/qerdsprovider` has with a QERDS access point. This
demo hosts a *reference* QTSP so the flow is visible locally; production would point at a
real NL QTSP (Cleverbase, Digidentity). It carries the same honesty caveat as the Domibus
bench: **proves the plumbing, not qualified compliance.**

> Context: the earlier "locked design spec" wayfinder map (#171) and its `.ai/plans/`
> signing docs were reverted off `main`; treat them as historical. This demo is a concrete,
> runnable artifact, not that design spec.

## What it is

`docker compose --profile signer up --build` brings up, alongside the normal dev stack:

| Service | Role |
|---|---|
| `signer-mysql` | MySQL 8 for the QTSP (credential + key state) |
| `qtsp-signer` | the reference QTSP **resource server**, built from source at a pinned upstream commit + a demo patch, with **SoftHSM2** inside as the QSCD stand-in |
| `qtsp-signer-provision` | one-shot: seeds a signing credential once the signer is healthy |

Then `docker/development/qtsp-signer/run-demo.sh` does a full **seed → `signHash` → verify**
round-trip: it mints a credential, signs a SHA-256 hash, and verifies the returned ECDSA
signature against the credential's certificate with `openssl`.

The build context is `docker/development/qtsp-signer/`. The image clones upstream at
`UPSTREAM_REF` (pinned) and applies `qtsp-demo.patch` (a `git diff` against that commit); the
`noauth` seed/health controller is a separate vendored file.

## Empirical proof (Phase 0)

Stood up the resource server against SoftHSM with **EJBCA and the authorization server both
out of the path** and drove a signature:

- `POST /csc/v2/signatures/signHash` → **HTTP 200** with a DER ECDSA signature.
- Independently verified with `openssl dgst -sha256 -verify` against the credential's
  self-signed certificate → **`Verified OK`**.
- Full path on SoftHSM: EC P-256 key-gen → key wrap → self-signed cert → DB persist →
  unwrap → raw ECDSA sign. The cert being self-signed confirms `signHash` never reads the
  certificate, which is *why* EJBCA is skippable.

## The demo patch (`qtsp-demo.patch`) and why each part exists

Upstream is coded against **Utimaco's** lenient PKCS#11 and always enrols certificates
against **EJBCA**. Three changes make it run on a laptop; each is marked at the call site.

1. **SoftHSM-portable PKCS#11 mechanisms** (`HsmService`, `CertificatesService`). SoftHSM is
   stricter than Utimaco:
   - Key wrap/unwrap: `CKM_AES_CBC` (no IV) → **`CKM_AES_KEY_WRAP_PAD`** (RFC 5649 — wrap +
     unwrap, arbitrary length, no IV). SoftHSM's AES-CBC has no `unwrap` flag and won't
     CBC-wrap a non-block-aligned key blob (`CKR_GENERAL_ERROR` at `WrapKey`).
   - Cert/CSR signing: combined `CKM_ECDSA_SHA256` → **pre-hash + raw `CKM_ECDSA`**
     (`CKM_ECDSA_SHA256` is `CKR_MECHANISM_INVALID` on SoftHSM). `signHash` already used raw
     ECDSA, so the signature path itself was unaffected — this only bit the cert path.
   - **A real vHSM/QSCD would not need this change; it is the software-HSM tax.** Worth
     upstreaming — it makes the reference impl run on any conformant token.
2. **EJBCA-free self-sign** (`ejbca.enabled=false`; `CertificatesService`, `EjbcaProperties`,
   `EjbcaService`, `CredentialsService`). When EJBCA is disabled the credential's certificate
   is **self-signed** (HSM-signing the TBS) instead of enrolled against a CA. Credential
   *creation* is the only place EJBCA was hard-wired; this branch removes it.
3. **`noauth` header-stub auth** (`NoAuthStubFilter`, plus the new `SpikeSeedController`).
   Under the `noauth` profile the resource server reads authorization claims from `X-Stub-*`
   headers and exposes `/spike/seed-credential` + `/spike/health`, so `signHash` can be
   driven without the OID4VP authorize flow. **DEMO ONLY — never enable `noauth` elsewhere.**

## Gotchas found (also useful if bumping the pin)

- **Upstream Dockerfiles' `COPY ../…` escapes the compose build context.** Their compose sets
  `context: .` (repo root) while the Dockerfile lives in a subdir, so `../pom.xml` points
  outside the context and the build fails before Maven. Our image builds from a clone with
  corrected paths, so this doesn't affect us — but it's why a naive `docker compose build`
  on the upstream repo fails.
- **`EjbcaProperties` `@NotNull`s** are inert (no `@Validated`), but the compose still
  supplies dummy EJBCA values so nothing NPEs while disabled.
- **SoftHSM slot id** is reassigned to a large value after `--init-token --free`; the
  entrypoint resolves it dynamically into `JACKNJI11_TEST_TESTSLOT`.

## What this demo does NOT cover (next phases)

- **The wallet ceremony.** The full visitor flow — `credentials/list → SCAL2 authorize via a
  wallet QR → signHash` — needs the upstream `authorization_server` (OID4VP) + a verifier
  (we already run the Yivi staging verifier for login). This profile runs the resource
  server **only, in `noauth`**: it shows the CSC signing mechanics, not the wallet-driven
  authorization. Adding the authorization server is the next phase.
- **Any backend wiring.** There is no signing seam / `CSCProvider` in the Go backend (the
  #171 design was reverted). This demo is a standalone hostable QTSP; wiring it behind a
  provider seam is separate work (relates to open tickets #148, #147, #151, #28).
- **Qualified anything.** Software HSM, self-signed certs, no EU trust list.

## Files

- `docker/development/qtsp-signer/Dockerfile` — pinned clone + patch + SoftHSM runtime.
- `docker/development/qtsp-signer/qtsp-demo.patch` — the three changes above (regenerate on a
  pin bump).
- `docker/development/qtsp-signer/src/SpikeSeedController.java` — `noauth` seed/health helper.
- `docker/development/qtsp-signer/docker-entrypoint.sh` — SoftHSM token init + slot resolve +
  `JACKNJI11_*` export.
- `docker/development/qtsp-signer/run-demo.sh` — seed → signHash → openssl verify.
- `compose.override.yaml` — the `signer` profile (`signer-mysql`, `qtsp-signer`,
  `qtsp-signer-provision`).
