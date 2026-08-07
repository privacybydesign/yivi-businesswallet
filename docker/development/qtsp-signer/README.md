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

This starts (alongside the normal dev stack): `signer-mysql`, `qtsp-signer` (built from the
pinned upstream commit + the demo patch, SoftHSM inside), and a one-shot
`qtsp-signer-provision` that seeds one signing credential. Then:

```bash
# 1) A credential was auto-seeded; seed another for a given subject if you like:
curl -s -X POST 'http://localhost:8085/spike/seed-credential?sub=alice' ; echo

# 2) Sign a SHA-256 hash with it (see run below for the full call), -> a base64 signature.
```

`run-demo.sh` in this directory does the full seed → `signHash` → **verify with openssl**
round-trip against `http://localhost:8085`.

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

## What this demo does NOT cover

- **The wallet ceremony.** The full flow — `credentials/list → SCAL2 authorize via a wallet
  QR → signHash` — needs the upstream `authorization_server` (OID4VP) and a verifier. This
  profile runs the **resource server only, in `noauth`**, which shows the CSC signing
  mechanics but not the wallet-driven authorization. Adding the authorization server is the
  next phase.
- **Qualified anything.** Software HSM, self-signed certs, no trust list.

## Bumping the upstream pin

1. Update `UPSTREAM_REF` in the `Dockerfile`.
2. Regenerate `qtsp-demo.patch` against the new commit (clone, apply the three changes,
   `git diff > qtsp-demo.patch`) and re-verify with `run-demo.sh`.
3. Consider upstreaming change #1 (the PKCS#11 portability fixes) — it makes the reference
   impl run on any conformant token, not just Utimaco.
