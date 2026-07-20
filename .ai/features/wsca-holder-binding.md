# WSCA-backed holder binding (personal + business wallets)

Status: **design** (not built). Repos: `irmago` (shared holder library),
`wallet-provider` (WSCA / SECDSA), `yivi-businesswallet` (business-wallet
backend), Yivi mobile app (personal wallet). Builds on
[`attestations.md`](./attestations.md) (holder engine) and
[`oid4vci-over-qerds.md`](./oid4vci-over-qerds.md) (receive flow).

## 1. Goal

The EUDI SD-JWT VC **holder-binding key** — whose public half goes into the
credential's `cnf` at issuance and which signs the KB-JWT at presentation — is
today a **software key** generated and stored (in the clear, per irmago's
SQLCipher finding) inside irmago. Move it into a **WSCA** (Wallet Secure
Cryptographic Application, split-key SECDSA over an HSM, provided by
`wallet-provider`) so the private key never lives in the wallet process.

irmago is the **shared holder library for both wallets**, so the change lands
there once and both consume it:
- **Personal wallet** (Yivi mobile app) — a person, EUDI "sole control" regime.
- **Business wallet** (this backend) — a legal person, self-managing under
  mandate.

## 2. Regulatory basis: business wallet needs no human-in-the-loop

From the European Business Wallets Regulation proposal (`regulation/`,
COM(2025) 838, 19 Nov 2025 — **a proposal; implementing acts pending**):

- **No "sole control".** The phrase appears **nowhere** in the Business Wallet
  act (unlike the personal EUDI wallet). Business wallets use a **role + mandate
  + access-control** model, not one-natural-person sole control. (Recital 55
  amended eIDAS Art 5a so the mandatory *personal* wallet is natural-persons-only
  — business wallets are a separate regime.)
- **Automation is a required feature.** Art 6(1)(d): wallets must support
  "interaction … **automatically without manual intervention or through direct
  user action**." A self-managing wallet is not merely allowed — it is mandated.
- **WSCA is mandatory for the back-end.** Annex §3(1): the back-end "**shall use
  at least one Wallet secure cryptographic application and … device** to manage
  critical assets." Annex §4(a): WSCA crypto ops run only for authenticated
  wallet users — satisfied for automation by the mandate/credential, not a live
  human.
- **Autonomy is under a mandate, fully audited.** Annex §12: authorisation by the
  acting subject's attestation/role/mandate scope, **real-time validated**, with
  **every execution event logged and bound to a cryptographically verifiable
  proof of authorisation** (Annex §12(2)(c), §7). Mandates are revocable,
  traceable; over-delegation/expiry auto-detected (Annex §12(3)).

**Conclusion:** the business wallet is **self-managing**; the human is in the
loop only at **mandate/authorisation setup** (recital 18, Art 5(1)(j)) and at
**secret rotation** — not per transaction. Per-action human approval is an
*optional org policy*, not a legal requirement. (Personal wallet is the opposite:
EUDI sole control → per-use human PIN.)

## 3. irmago constraints (non-negotiable)

1. **Backwards compatible — additive only.** The WSCA seams (irmago #614) are
   *opt-in*: `openid4vci.NewClient` takes `WithHolderKeyBinder(...)`,
   `eudi_sdjwt_dcql.NewSdJwtVcDcqlHandler` takes a variadic `KeyBinder`,
   `eudi/wallet.Config` exposes `HolderSigner`/`IssuanceKeyBinderFactory`. **When
   unset, behavior is identical to today (software keys).** The holder key-metadata
   PK fix (#623) is likewise additive (see [its analysis](../features/attestations.md)
   / irmago #623 — `AutoMigrate` keeps the old unique index; no data migration).
2. **One library, both wallets.** irmago stays **wallet-agnostic**. The
   personal-vs-business and WSCA-vs-software differences live **entirely in the
   injected `HolderSigner`/`HolderKeyBinder` implementation and each app's
   wiring**, never in irmago core. irmago gains **no `secdsa` dependency**.

Combined base branch: `feat/eudi-holder-wsca-and-pk-fix` (merges #623 + #614 +
the #622 `sqlcipherstorage.New` compat fix).

## 4. The shared seam

irmago #614 abstractions (stdlib types only):
- `eudi/wallet.HolderSigner`: `GenerateKeys(num) (refs, pubs, err)`,
  `SignES256(ref, signingInput) (rawSig, err)`, `Reference(pub) (ref, err)`,
  `Remove(refs)`. Default impl `SoftwareHolderSigner` = today's behavior.
- `eudi/openid4vci.HolderKeyBinder` (issuance PoP), `sdjwtvc.KeyBinder` bridge
  (`newSignerKeyBinder`), `proofs.BuildWithES256Signer`.
- `holdersigner.derToRawES256` already converts the WSCA's DER `(r,s)` to JWS raw
  form — the exact shape `walletmobile.Sign` returns.

**The PIN is absent from every irmago interface** (deliberately — irmago has no
secdsa/PIN concept). The WSCA-backed signer threads the PIN/secret in
**out-of-band**, per session.

## 5. The `irmawsca` adapter (in `wallet-provider`, to be written)

Implements irmago's `HolderSigner` + `openid4vci.HolderKeyBinder` over
`mobile/walletmobile`:

| irmago | walletmobile |
|---|---|
| `GenerateKeys(n)` | `GenerateKey(keyID, pin)` × n |
| `SignES256(ref, in)` | `Sign(keyID, in, pin)` → `derToRawES256` |
| `Reference(pub)` | JWK thumbprint → keyID |
| `Remove(refs)` | `RemoveKey(keyID, pin)` |

The adapter holds the PIN/secret for the session (constructor/context). The WSCA
`Sign` hashes the raw message server-side, yielding a **standard ES256/P-256
signature** — issuers/verifiers need no SECDSA awareness.

## 6. PIN / secret model — differs per wallet

### Personal (mobile) — human PIN, sole control, per-use
1. User enters **one PIN**.
2. **Verify at the keyshare server first** (existing mechanism; a gate so wrong
   PINs don't burn WSCA attempts). The keyshare PIN is never raw on the wire —
   the app sends `base64(sha256(nonce‖pin))`; the app holds the raw PIN.
3. Pass the **same raw PIN** to `walletmobile` (`Activate`/`Sign`). "Same PIN" =
   same human string; each system verifies independently — they **cannot** share
   verification (keyshare = device-nonce-salted hash, no raw-PIN API; WSCA = local
   crypto knowledge factor).

### Business (backend, headless) — sealed secret, autonomous between setup/rotation
No human at sign time, so the secret is stored **encrypted at rest** and
decrypted in-memory only at `Sign`.

- **Prefer a high-entropy secret** over a weak human PIN (nobody memorises it for
  daily use) — within `walletmobile.IsValidPIN` constraints (verify: reportedly
  "≥5 ASCII digits" → may be digits-only; if so, a long random digit string).
- **Envelope at rest, reusing the PostGuard pattern** (`internal/crypto/cipher.go`
  + `internal/postguard/crypto.go`): a KMS/secret-manager **master key** (e.g.
  `ATTESTATION_HOLDER_WSCA_KEK`) wraps a **per-org DEK**; the DEK seals the
  secret; ciphertext lives in Postgres. Master key never in the DB.
- **Lifecycle (human in the loop exactly twice):**
  - **Setup:** org owner/representative enters the secret → `walletmobile.Activate`
    (3-round SECDSA) → seal the secret. This is the mandate/authorisation moment.
  - **Autonomous:** issue/receive decrypt-in-memory per `Sign`, under mandate +
    logging. Never log the secret (matches walletmobile's own note).
  - **Rotation / cycling:** representative enters a new secret →
    `walletmobile.ChangePIN(current, new)` → re-seal. WSCA account = `hash(U)`
    survives rotation; a fresh certificate is issued, old retained for historical
    verifiability.
- **U (possession key) custody:** prefer a **server HSM**
  (`NewWalletWithHardwareSigner`) over the JKS software keystore, so both factors
  aren't plain files.

**Trade-off (be clear-eyed):** storing the secret server-side collapses SECDSA's
two factors into one server-held bundle — **not "sole control"**. That is
acceptable for a business wallet (no sole-control requirement; WSCA/WSCD required)
but security then rests on the **KMS-held master key**, **HSM-held `U`**, and the
**access-control + mandate + proof-of-authorisation + logging** the regulation
already mandates.

## 7. Business-wallet backend pieces (this repo)

- **Config:** `ATTESTATION_HOLDER_WSCA_KEK` (KMS), wallet-provider base URL.
- **Per-org WSCA store** (new table): wrapped DEK, sealed secret, WSCA
  account/cert id, `activated_at`, `rotated_at` (+ rotation-due). Envelope helpers
  reuse `postguard/crypto.go`.
- **Org-admin UI:** *Activate* (enter secret) and *Rotate* (current + new) flows.
- **Wiring:** inject the `irmawsca` adapter as the `HolderSigner`/`KeyBinder` for
  the `eudiholder` engine when WSCA is configured; software keys remain the
  default (backwards compatible).
- **Mandate / proof-of-authorisation:** bind each autonomous action to its
  authorising mandate and record the cryptographic proof in the audit log
  (regulatory Annex §12(2)(c)) — design TBD.

## 8. Sequencing

1. ✅ irmago combined branch `feat/eudi-holder-wsca-and-pk-fix` (builds).
2. `irmawsca` adapter in `wallet-provider` (+ finish the live E2E the #614 doc
   left blocked, now that staging is up: `https://wallet-provider.staging.yivi.app`).
3. Personal wallet: mobile PIN → keyshare-verify → WSCA holder binding.
4. Business wallet: WSCA secret store + Activate/Rotate + wire adapter into
   `eudiholder`.

## 9. Open questions

- `walletmobile.IsValidPIN` charset (digits-only?) → business-secret policy.
- `U` custody server-side (server HSM vs JKS) for the business wallet.
- Rotation policy: time-based, event-based (operator change / compromise), or both.
- How each autonomous action records its mandate + proof-of-authorisation (Annex
  §12) — the compliance-critical binding.
- Live E2E vs the staging wallet-provider (the #614 doc's blocked `finalize` step).
- COM(2025) 838 is a **proposal** — reconfirm conformance against the adopted act
  + implementing acts before go-live (legal review, not just this read).
