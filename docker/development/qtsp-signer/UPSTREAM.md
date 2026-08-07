# Upstreamable findings — EUDI wallet-driven / RP-centric QTSP reference impl

Issues found while running
[`eudi-srv-web-walletdriven-rpcentric-signer-qtsp-java`](https://github.com/eu-digital-identity-wallet/eudi-srv-web-walletdriven-rpcentric-signer-qtsp-java)
against **SoftHSM2** instead of Utimaco. These are **portability/build bugs in the reference
implementation itself**, independent of our demo — worth reporting/PRing upstream. Pinned
commit: `8d07f96bf19e4ffa97ff2c469afc4d9cf38d3028`.

The reference impl is coded against Utimaco's PKCS#11, which implements optional mechanisms
and lenient behaviours that SoftHSM (a strict, widely-used software token) does not. Two of
the three below are **not SoftHSM-specific** — they will bite *any* PKCS#11 token that
implements only the mandatory mechanism set, which the standard explicitly permits.

---

## 1. Private-key wrap/unwrap uses `CKM_AES_CBC`, which is not a wrapping mechanism

**Symptom:** `CKR_GENERAL_ERROR (0x5)` thrown from `C_WrapKey` during credential creation
(`CredentialsService.createECDSAP256Credential` → `HsmService`), before any signing.

**Root cause:** the private key is exported under the instance AES secret key with
`CKM_AES_CBC` and no IV/parameter. On SoftHSM:
- an AES key created for wrapping has no `CKA_UNWRAP`/`CKA_WRAP` semantics for CBC in the way
  the code assumes, and
- CBC cannot wrap a non-block-aligned encoded private key without padding.

`CKM_AES_CBC` is a bulk-encryption mechanism, not a key-wrap mechanism; using it for
`C_WrapKey`/`C_UnwrapKey` is outside its intended contract.

**Location:** `HsmService` — 4 call sites (two `C_WrapKey`, two `C_UnwrapKey`), P-256 and RSA
paths.

**Fix:** use **`CKM_AES_KEY_WRAP_PAD` (`0x210a`, RFC 5649)** — a proper key-wrap mechanism
that wraps+unwraps arbitrary-length keys with a default AIV (no IV parameter needed).
Supported by SoftHSM **and** Utimaco, so the change is safe on the current target too.

```java
// before
CE.WrapKey(session, new CKM(CKM.AES_CBC), secretKeyObj, privKey.value());
// after
private static final long CKM_AES_KEY_WRAP_PAD = 0x210aL;
CE.WrapKey(session, new CKM(CKM_AES_KEY_WRAP_PAD), secretKeyObj, privKey.value());
```

---

## 2. Certificate/CSR signing uses the combined `CKM_ECDSA_SHA256`, an optional mechanism

**Symptom:** `CKR_MECHANISM_INVALID (0x70)` when the certificate path signs on the HSM.

**Root cause:** `HsmService.signDTBSWithECDSAAndSHA256` maps `"SHA256WITHECDSA"` to
`CKM_ECDSA_SHA256` (`4164`), the *combined* hash-and-sign mechanism, and the verify path uses
`CKM_ECDSA_SHA256` likewise. This method is on the **real EJBCA CSR-signing path**
(`CertificatesService.generateCertificateRequest`), so this is not demo-only — it affects
normal credential creation on any token without the combined mechanism.

`CKM_ECDSA_SHA256` (hash+sign in one call) is **optional** in PKCS#11; SoftHSM implements only
the mandatory raw `CKM_ECDSA` (`4161`). `signHash` is unaffected because it already uses raw
`CKM_ECDSA`.

**Location:** `HsmService` — the sign alg map (`"SHA256WITHECDSA" -> 4164L`), its
`SignInit`, and the ECDSA `VerifyInit` (`CKM.ECDSA_SHA256`).

**Fix:** hash the DTBS in software (`MessageDigest.getInstance("SHA-256")`) and sign/verify the
digest with raw `CKM_ECDSA` (`4161`). The result is a byte-identical `ecdsa-with-SHA256`
signature; works on tokens with or without the combined mechanism.

```java
byte[] digest = MessageDigest.getInstance("SHA-256").digest(tbsBytes);
// SignInit with CKM_ECDSA (4161) over `digest`
```

---

## 3. Dockerfiles `COPY ../…`, which escapes the docker-compose build context

**Symptom:** `docker compose build` fails with a "forbidden path outside the build context"
/ COPY error, before Maven runs.

**Root cause:** `docker-compose.yml` sets `context: .` (repo root) with
`dockerfile: resource_server/Dockerfile`, but the Dockerfile does `COPY ../pom.xml .` etc.
COPY sources are resolved relative to the **build context root**, so `../` points outside it.

**Location:** `resource_server/Dockerfile` and `authorization_server/Dockerfile` (all `COPY ../…`
lines).

**Fix:** with `context: .` at the repo root, drop the `../`: `COPY pom.xml .`,
`COPY resource_server/pom.xml ./resource_server/pom.xml`, etc.

---

## Not for upstream (our demo scaffolding, intentionally weakens security)

These live in our demo patch but should **not** be proposed upstream:

- **EJBCA-free self-sign** (`ejbca.enabled=false`) — bypasses the CA; only to run without EJBCA.
- **`noauth` header-stub** + `/spike/seed-credential` — disables authentication.
- **Dynamic SoftHSM slot-id resolution** in the entrypoint — a deployment concern (SoftHSM
  reassigns the slot id after `--init-token --free`), not an app bug; at most a docs note.
