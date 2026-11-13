# Feature: Wallet unit attestation (WE BUILD CS-05)

**Status:** built (YIV-304). The provider issues the attestations (part 1), and the holder
sends them to an issuer when an organization receives a credential (part 2).

## What it is

- The backend is the **Business Wallet Provider** for every organization it hosts. It
  attests an organization's **wallet unit** (CS-05 BWU, the wallet service instance) so an
  issuer or another business wallet can check it is a real business wallet, from a known
  provider, run for a named organization.
- A BWUA is two short-lived JWTs, both ES256 with the provider chain in `x5c` and no `iss`:
  - **BWIA** (`oauth-client-attestation+jwt`): `wallet_name`, `wallet_version`,
    `wallet_link`, optional `wallet_solution_certification_information`, `ebwoid_id` (the
    org EUID), `legal_name`, `cnf` (instance key) and `client_status`.
  - **SKA** (`key-attestation+jwt`): `attested_keys`, `key_storage`,
    `user_authentication`, `certification` (a URL string, not an object) and
    `key_storage_status`.
- Code: `backend/internal/walletunit`. `Provider.Issue` is the entry point.

## Why it is built this way

- **No SKA for software keys.** An SKA claims HSM key storage. It is issued only when the
  caller says the keys are in the WSCA *and* the deployment configures the HSM
  certification (`WALLET_UNIT_SKA_*`). Anything else would be a false claim.
- **Status is derived, not stored per entry.** `wallet_unit_attestations` keeps only the
  index; the list renders each entry from its unit and org:

  | Unit | Org | Entry |
  |---|---|---|
  | revoked | any | invalid |
  | active | deleted or revoked | invalid |
  | active | suspended | suspended |
  | active | active | valid |

  So org cessation and suspension need no call into this package.
  `wallet_units.organization_id` is `ON DELETE SET NULL`, not cascade, so a deleted org's
  entries stay published as invalid until they expire.
- **Random indices, never reused.** 16 lists of 2^17 entries share one index space, 2 bits
  per entry (valid / invalid / suspended). A random index tells an issuer nothing about
  how many units exist. Recycling after `status_expires_at` is not built.
- **Revocation is per unit.** An admin revokes for `owner_request` or `key_compromise`.
  `Issue` then refuses (`ErrUnitRevoked`) until an admin activates a new unit, so a
  compromised unit is not silently replaced. The first unit is created by the first
  `Issue`.
- **Lifetimes:**
  - token `exp` 1 hour (CS-05: under 24 h, single-use);
  - status `exp` 90 days (CS-05: at least 31 days ahead at presentation).
- **Status lists live under `/api/v1`** (`GET /api/v1/wallet-unit/status-lists/{n}`,
  public, `application/statuslist+jwt`). The Vite dev proxy forwards only `/api`, so a
  root path would hit the SPA in dev. The URI is built from `APP_BASE_URL`.

## Sending them

- `eudiholder.Engine.SetWalletUnitAttester` hands irmago a `WalletAttester` per redemption
  (`eudiholder/walletunit_attester.go`). irmago then (privacybydesign/irmago#800):
  - authenticates at PAR and token with the BWIA (`OAuth-Client-Attestation` + PoP),
    `client_id` = the BWIA `sub`;
  - binds the access token with DPoP under the attestation key;
  - puts the SKA in the `key_attestation` header of each `jwt` proof;
  - asks for one credential per request, since an SKA is per key.
- **A fresh instance key per redemption.** Each BWIA is single-use and never shared across
  issuers, so two issuers cannot link the org's sessions by its key.
- **An org that cannot be attested still receives.** No EUID, an inactive org or a revoked
  unit wrap `ErrNotAttestable`: the redemption goes without an attestation and logs a
  warning. Issuers that require one refuse it themselves.
- **SKA only for HSM keys.** With software keys (no WSCA) `KeyAttestation` returns none.

## Configuration

- `WALLET_UNIT_PROVIDER_CERT` / `WALLET_UNIT_PROVIDER_KEY`:
  - PEM chain (leaf first, `digitalSignature`) and its P-256 key, set together;
  - unset mints an ephemeral self-signed certificate at boot, which nobody trusts and
    which changes on restart.
- `WALLET_UNIT_VERSION`: the BWIA `wallet_version` (default `unreleased`).
- `WALLET_UNIT_CERTIFICATION_INFO`: the wallet solution certification, a JSON object. CS-05 requires it,
  and there is none yet, so it stays empty.
- `WALLET_UNIT_SKA_CERTIFICATION_URL`, `WALLET_UNIT_SKA_KEY_STORAGE`,
  `WALLET_UNIT_SKA_USER_AUTHENTICATION`: all three set, or none (no SKA).

## Not built

- **Trust.** The provider certificate is not on the WE BUILD Wallet Provider Trust List
  (YIV-518). Until it is, a relying party can only trust it by adding the root.
- CS-05 discovery registry (Annex C) and the throughput profile.
- Revocation when the provider itself drops off the trusted list: not ours to trigger.
