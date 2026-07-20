# WSCA-backed holder binding

Status: backend + org-admin UI landed; live end-to-end against a staging
wallet-provider still pending. Opt-in — off by default.

## Why

The business wallet holds received attestations (SD-JWT VCs). Each held credential
is bound to a holder key that signs the OpenID4VCI proof-of-possession at
redemption and, later, key-binding on presentation. By default those holder keys
are **software** keys in the irmago EUDI storage. Under the European Business
Wallets Regulation the holder key material must live in a WSCA/WSCD under sole
control (Annex §3). WSCA-backed binding puts the holder key in the wallet-provider
HSM via SECDSA: the private key never enters this process.

This is a per-organization autonomous wallet — there is no per-transaction human
(the regulation mandates automation, Art 6(1)(d)). The only human-in-the-loop
moments are **activation** and **secret rotation**, done by an org admin.

## Pieces

- **wallet-provider** (`secdsa` module, private repo) — the WSCA. `secdsa` is the
  split-key SECDSA core; `mobile/walletmobile` is the client (`Activate`,
  `ChangePIN`, `CertificateID`, `IsActivated`); `mobile/walletmobile/irmabinding`
  adapts it to irmago's holder-binding signer interface.
- **irmago `eudi/holderkeys`** — the CGO-free holder-signer seam extracted from
  `eudi/wallet` so this backend (built `CGO_ENABLED=0`, and `go test -race` which
  forces CGO on) never pulls `sqlcipherstorage`/libsqlcipher. `irmabinding`
  depends on `holderkeys`, not `eudi/wallet`.
- **`internal/eudiholder`** — the holder engine. `WSCAConfig` + `SetWSCA` enable
  WSCA binding on the **redeem path**: `holderKeyBinder` opens the org's
  already-activated `walletmobile` wallet, decrypts its sealed secret, and builds
  an `irmabinding` issuance binder. `nil` WSCAConfig → software keys (backwards
  compatible). `OrgKeystoreDir(base, orgID)` is the single source of truth for the
  per-org keystore path — the redeem path and the activation flow MUST agree.
- **`internal/wsca`** — the sealed-secret store. One row per org
  (`org_wsca_accounts`): the activation secret sealed with AES-256-GCM under the
  deployment KEK (`ATTESTATION_HOLDER_WSCA_KEK`), plus `account_id` (stable) and
  `certificate_id` (rotates). CGO-free. `Secret(ctx, orgID)` is what the redeem
  path calls; the secret is **never logged**.
- **`internal/wscawallet`** — the org-admin activation/rotation service +
  HTTP handler. `Activator` drives `walletmobile.Activate`/`ChangePIN` (behind the
  `WalletClient` interface so it is unit-testable without a live WSCA) and seals
  via `wsca.Store`. `Configured()` = WSCA enabled (a wallet-provider URL is set)
  **and** a KEK is present.

## Activation / rotation flow

Org-admin only (`RequireOrgAdmin`), under `/api/v1/orgs/{slug}/wsca`:

- `GET /wsca` → `{configured, activated, account?}`. `configured:false` when WSCA
  is off on this deployment; the UI shows an informational card.
- `POST /wsca/activate {secret}` → runs the one-time SECDSA activation and seals
  the secret. `secret` is the SECDSA knowledge factor (≥5 ASCII digits, mirrors
  `secdsa.MinPINLength`). 409 if already activated, 503 if not configured.
- `POST /wsca/rotate {currentSecret, newSecret}` → proves the current secret and
  re-keys the certificate against the new one (SECDSA `ChangePIN` keeps the
  possession key U; a fresh certificate is issued), then reseals. The account and
  held credentials are preserved. 409 if not activated.

`account_id` is set to the first certificate id at activation and kept across
rotation (walletmobile exposes only the certificate id, not the derived account
hash); `certificate_id` tracks the latest.

Frontend: Settings → **Wallets** tab (`WscaWalletPanel`). Activate form (secret +
confirm), or, when activated, the account card + a rotate form. The secret is
entered here once, sealed, and used autonomously thereafter — re-entered only to
rotate.

## Config

- `ATTESTATION_HOLDER_WSCA_URL` — wallet-provider base URL. Setting it enables the
  feature (requires `ATTESTATION_HOLDER=irmago` and the KEK).
- `ATTESTATION_HOLDER_WSCA_KEK` — hex 32-byte deployment key sealing each org's
  secret at rest. Rotating it makes existing sealed secrets undecryptable.
- `ATTESTATION_HOLDER_WSCA_KEYSTORE_DIR` — parent dir for per-org walletmobile
  keystores (a persistent volume in prod).
- `ATTESTATION_HOLDER_WSCA_INSECURE` — trust the wallet-provider dev TLS cert
  (local/staging only).

## Dependency note

`backend/go.mod` currently `replace`s `secdsa` with a pinned wallet-provider
pseudo-version, and CI accesses the private repo via a deploy key
(`WALLET_PROVIDER_DEPLOY_KEY` + `GOPRIVATE`). The irmago `holderkeys` split is
upstream (see irmago changes); repin to the merged versions before final merge.

## Open items

- Live E2E against `wallet-provider.staging.yivi.app` (cannot be done headless):
  activate → receive an offer over QERDS → confirm the redeem path signs the
  proof with the WSCA key.
- Rotation error mapping: a wrong `currentSecret` currently surfaces as a generic
  500 (walletmobile does not return a typed error we can distinguish from a
  transport failure). Tighten once walletmobile exposes typed errors.
