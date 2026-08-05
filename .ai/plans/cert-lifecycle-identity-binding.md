# Certificate lifecycle & identity binding

Status: **proposed** (design only — no production code). Issue [#178](https://github.com/privacybydesign/yivi-businesswallet/issues/178), a grilling ticket under the qualified signing & sealing design map [#171](https://github.com/privacybydesign/yivi-businesswallet/issues/171).

Feeds on closed research [#172](https://github.com/privacybydesign/yivi-businesswallet/issues/172) (CSC + NL QTSP landscape), and sits on top of the locked seam ([`.ai/plans/signing-provider-seam.md`](./signing-provider-seam.md), #175), the sole-control model ([`.ai/plans/sole-control-authorization.md`](./sole-control-authorization.md), #176) and the format decision ([`.ai/plans/ades-format-dss-boundary.md`](./ades-format-dss-boundary.md), #177). It locks *how a signing/sealing certificate comes to exist, how a signer references it, and how each certificate binds to the wallet's identity model* — **leaning entirely on the QTSP, never building a registration authority** (the map rules an RA out of scope).

## Why this is a decision and not a copy of the research

#176 deferred *identity-to-certificate matching* to this ticket. #172 established the market facts (production QTSPs are on CSC v1 with pre-provisioned credentials; short-lived certs are a CSC v2 concept with low-confidence wiring; seals are frequently a separate proprietary credential). This ticket turns those into a locked shape: which lifecycle v1 uses, how a person's credential and an org's seal credential each bind to our `organization` model, and exactly what the wallet stores versus what stays at the QTSP.

## 1. Pre-provisioned credentials for v1; on-the-fly deferred

**Decision: v1 uses pre-provisioned qualified credentials, referenced by a CSC `credentialID` (or, for a proprietary seal, an autosigner id). On-the-fly / short-lived certificates are deferred.**

- Production NL QTSPs (Cleverbase, Digidentity-person) are on **CSC v1 1.0.4.0**, where a credential is a **persistent** enrolled thing discovered via `credentials/list` + `credentials/info` — exactly the `Credentials()` → `Credential{ID, …}` shape the #175 seam already lists. This is what v1 targets.
- **Short-lived / "one-shot" credentials** (CSC v2's `signatureQualifier`, where the RSSP mints a cert on the fly bound to a freshly-proofed identity, increasingly via an EUDI presentation *to the QTSP*) are **deferred**: they are CSC **v2**, the exact field wiring is low-confidence in the research, and the identity-proofing-at-signing they depend on is part of the same pre-GA wallet-as-factor direction #176 deferred. They are a **forward-compatible** extension, not a v1 path.
- **Seam-forward note:** the #175 seam authorizes against a `CredentialID`. An on-the-fly flow authorizes against a `signatureQualifier` instead, so adding it later means the seam accepts a qualifier-or-id in `AuthorizationRequest` and `Credentials()` may synthesize a qualifier-backed `Credential`. That is an additive change behind the same interface; v1 stays on enumerated `credentialID`s and does not pay for it now.

## 2. How a signer references a certificate — person vs org-seal, and the mapping to our identity model

Two certificate kinds, two very different bindings. This is the core of the ticket.

### 2.1 Person-signing certificate (QES)

**Decision: a person's signing certificate belongs to the *person*, is referenced by a QTSP `credentialID`, and is bound to the business-wallet user by a one-time link step that matches the user's OpenID4VP-disclosed identity to the certificate subject.**

- The credential belongs to the **natural person**, not the org. It is discovered via `Credentials()` for `SubjectRef{Kind: signature, SubjectID: <session user>}` (#175). The org dimension is *only* the membership + the `signing:sign` RBAC gate (#176) — the certificate is never org-owned.
- **The binding problem #176 handed here:** how does the wallet know *which* QTSP credentials belong to *this* user, and that they are genuinely the same natural person? The wallet needs the QTSP's own user handle (`credentials/list` takes a `userID`) mapped to our user. That mapping is established once, at a **per-user QTSP linking step**:
  1. The user runs the QTSP's own authentication (an OAuth2 authorization-code flow — Cleverbase's model — or the QTSP app's push consent — Digidentity's). We obtain the QTSP `userID`/subject reference. This is the QTSP owning identity proofing; we do not re-proof.
  2. **We require an OpenID4VP identity disclosure (`ScopeIdentity`: passport-or-id + email + phone) at link time and match the disclosed legal identity against the certificate subject** returned by `credentials/info`. A match is required to store the link; a mismatch blocks it. This is the concrete answer to #176's deferred "matching": it happens **once at link time, not per signature**, so the signing path stays simple (it uses an already-verified link).
  - Match on the **strongest available identifier**: a national/serial identifier where both the EUDI attestation and the X.509 subject carry a comparable one, else normalized name (+ date of birth where present). Where the certificate exposes no machine-comparable identifier, the match is recorded as **name-only / advisory** and the link requires org-admin confirmation rather than silently trusting it. The match outcome is recorded in `audit`/evidence either way.
- **What is stored:** the `(business-wallet user, QTSP/driver, QTSP userID, credential subject, match outcome)` link — a central, per-user record (users are central in this deployment, §multi-tenancy), never key material. At sign time the flow uses this link; the certificate chain itself is authoritative from the QTSP per signature (echoed in `SignReceipt.Signatures[].Certificate`, #175) and only cached for display.

### 2.2 Organisation-seal certificate (QESeal)

**Decision: a seal certificate belongs to the *organisation*, is a per-org pre-provisioned credential (a CSC `credentialID` or a proprietary autosigner id), provisioned once at onboarding by an authorised representative, and cross-checked against the org's registration on record.**

- The credential belongs to the **legal person (the org)**, referenced by `SubjectRef{Kind: seal, SubjectID: <org>}` (#175). Its X.509 subject carries the org's identifiers (name, registration/KvK number, possibly LEI).
- Per #172 a seal is frequently a **separate product/endpoint** (Digidentity's eSeal **Autosigner**, `POST /v1/auto_signers/{id}/sign`, is proprietary — not CSC). The #175 seam already absorbs this: a seal driver may speak a non-CSC protocol invisibly, and the *reference* the wallet stores for a seal is therefore whatever that driver needs (a CSC `credentialID` **or** an autosigner id), not necessarily a CSC credential.
- **Onboarding binding:** the org's seal is provisioned once with the QTSP by an **authorised representative** — the QTSP verifies the org and the representative's mandate (this is the RA work we explicitly do not build). The wallet's part is to **cross-check the seal certificate's org registration number against the org's stored registration** (this deployment already treats KvK as an authentic source), record which member provisioned it, and gate all *use* behind `signing:seal` (#176). A registration mismatch blocks provisioning.
- **What is stored:** the per-org seal credential reference + which QTSP/driver + the provisioning representative + the registration cross-check outcome. Never key material. (The broader shape of *per-org provider configuration* — one tenant on Cleverbase, another on Digidentity — remains the map's sign-and-deliver concern, #175; this ticket fixes only the identity-binding fields, not the whole config surface.)

### 2.3 Why the two bindings differ

A signature's subject *is* the actor, so its binding is a per-user link verified against the acting human's disclosed identity. A seal's subject is a principal *other than* the acting human (the org), so its binding is an org-level provisioning fact verified against the org's registration, and its per-act control is the `signing:seal` mandate rather than an identity match. This mirrors the split #176 drew for authorization.

## 3. The onboarding hand-off: wallet stores references, QTSP holds everything qualified

**Decision, stated as an invariant:**

| Held by the QTSP | Stored by the wallet |
|---|---|
| The **private key**, in its QSCD/HSM — never leaves it | A **reference** to the credential (CSC `credentialID` / autosigner id) |
| The **qualified certificate** + chain (authoritative) | Cached credential **metadata** for display (subject, validity, SCAL, `AuthMode`, cert chain) — refreshed from `credentials/info`, never treated as authoritative |
| All **identity proofing / enrollment** (the RA role) | The **identity-binding link** (§2.1 person link, §2.2 org provisioning record) + the match/cross-check outcome |
| Certificate **issuance, renewal, revocation** | Nothing — the wallet reads validity from `credentials/info` and surfaces expiry; it does not issue, renew or revoke |

The wallet **never** holds key material and **never** performs identity proofing or certificate issuance. Certificate *lifecycle* (issue/renew/revoke) is the QTSP's; the wallet only **reads** validity (from `Credential.ValidFrom`/`ValidUntil`, #175) to avoid offering an expired credential and to show status. This is the same "we are a requestor, not an authority" stance the whole map takes.

## Decisions settled (against the three the ticket asked for)

1. **Pre-provisioned vs on-the-fly.** Pre-provisioned CSC-v1-style credentials for v1; short-lived / `signatureQualifier` certs deferred as an additive, forward-compatible extension (§1).
2. **How a signer references the cert; person vs org-seal, and the mapping to memberships / org identity.** Person cert = QTSP `credentialID` owned by the person, bound to the user by a one-time link with a **link-time OpenID4VP identity match to the cert subject** (org linkage is membership + `signing:sign` only). Org-seal cert = per-org pre-provisioned credential (CSC id or proprietary autosigner id) owned by the org, provisioned by an authorised representative, cross-checked against the org's registration, use gated by `signing:seal` (§2).
3. **The onboarding hand-off — wallet vs QTSP.** QTSP holds key + cert + proofing + issuance/renewal/revocation; wallet stores references, cached metadata, and the identity-binding link only — never key material, never an RA (§3).

## Deferred (boundaries named for the next tickets)

- **On-the-fly / short-lived credential issuance** (CSC v2 `signatureQualifier`) — a forward extension once a QTSP and the v2 wiring are confirmed (§1).
- **Per-org provider configuration** surface (which driver/QTSP per org, endpoints, secrets) — the map's sign-and-deliver concern (#175); this ticket fixes only the identity-binding fields.
- **The concrete linking-flow UX and storage schema** — execution, once the map's decisions land; this ticket fixes what is bound and against what, not the handler/table shapes.
- **Inbound certificate/trust-anchor validation** — #179 (it validates *received* signers' certs against trusted lists; this ticket is about *our* signers' credentials).

## Verification

Doc-only; no build or test surface is affected. Consistent with the locked seam — it adds no method or type to `signingprovider` and relies only on what the seam already carries (`Credentials()`, `SubjectRef`, `Credential.{ID, Certificate, ValidFrom, ValidUntil, SCAL, AuthMode}`). The one new persistent concept — the per-user QTSP link and the per-org seal provisioning record — lives in `internal/signing`'s stores, not the seam, and reuses the existing OpenID4VP `ScopeIdentity` disclosure and the KvK authentic-source cross-check the deployment already has.

## Harvest

- Convention to add or update in `.ai/conventions/<area>.md`? **none.**
- Feature doc to write or update in `.ai/features/<name>.md`? **`.ai/features/signing.md`** — written by the map's capstone ticket, which folds #175–#179 into one architecture doc. This plan is the interim source of truth for the certificate-lifecycle & identity-binding decision.
