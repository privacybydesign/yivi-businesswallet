# Sole-control / SCAL2 authorization model for QES & QESeal

Status: **proposed** (design only — no production code). Issue [#176](https://github.com/privacybydesign/yivi-businesswallet/issues/176), a grilling ticket under the qualified signing & sealing design map [#171](https://github.com/privacybydesign/yivi-businesswallet/issues/171).

Feeds on the closed research [#174](https://github.com/privacybydesign/yivi-businesswallet/issues/174) (eIDAS-2 / EUDI remote-QES sole-control direction) and [#172](https://github.com/privacybydesign/yivi-businesswallet/issues/172) (CSC + NL QTSP landscape), and sits directly on top of the locked seam in [`.ai/plans/signing-provider-seam.md`](./signing-provider-seam.md) (#175). It locks *how sole control is achieved per signature and per seal*; it defers *how a verified identity binds to a certificate* to [#178](https://github.com/privacybydesign/yivi-businesswallet/issues/178) and *what document/signature format is produced* to [#177](https://github.com/privacybydesign/yivi-businesswallet/issues/177).

## Why this is a decision and not a copy of the research

#174 already recommends QTSP-owned sole control for v1. This ticket exists because the *recommendation* is not yet a *model*: it does not say what role our OpenID4VP identity layer plays in a ceremony, how a seal — which carries no natural-person will — is authorised, or what the sole-control audit trail must contain. Those are the load-bearing shapes the next tickets (#177 format, #178 identity binding) and the sign-and-deliver / ceremony-UX fog all hang off, so they are locked here.

## 1. The model: QTSP-owned sole control (v1)

**Decision: v1 is QTSP-owned sole control. We are the orchestrator, never the activation factor.**

We drive the CSC `authorize` flow; the QTSP's Signature Activation Module (SAM) collects the activation factor and verifies the SCAL2 binding of **signer-authentication ⨯ signing-key ⨯ DTBS/R** inside its HSM (EN 419241-1/-2). Our wallet stays entirely outside the QSCD/SAM certification boundary.

Why, in one line each (full weighing in #174):

- It is the **only certified-in-production model today** — every live NL/EU QTSP (QuoVadis/DigiCert, Digidentity, Evidos/Entrust) enforces SCAL2 with its own SAM and own factor.
- It is the **smallest delta** from what we already have (OpenID4VP login + a config-swappable provider seam), and keeps the certified QSCD boundary the QTSP's problem.
- **Wallet-as-activation-factor** (the user's EUDI wallet supplies the SAD) is eIDAS-2-mandated and ARF-defined but pre-GA (2025–26 pilots). It is **deferred to a future driver behind the same seam**, not a re-architecture: the upgrade adds an `AuthMode`/driver that produces the SAD wallet-side, and the domain slice — which already branches on `Authorization.Status`, never on how the factor was collected — does not change. This is exactly what #175's seam was shaped for.

## 2. Where our OpenID4VP / session layer sits in a ceremony

Framing that matters: in this app **we are the RP/orchestrator, not a first-party wallet**. The user's EUDI wallet (their phone) discloses to our hosted verifier to *log them into the business wallet*; the backend is `auth.RequireUser` + `organization.Authorize` + org RBAC. A signing ceremony runs on top of that session.

**Decision: in v1 the OpenID4VP/session layer only authenticates and RBAC-gates the *requesting user*. The sole-control activation is entirely the QTSP's, driven through the seam's `AuthMode` leg. Our OpenID4VP contributes nothing to the SAD.**

Concretely:

- The session establishes **who is driving the ceremony** (`auth.RequireUser`) and the org context (`organization.Authorize`); the RBAC gate (§3/§4) decides whether that user may start a signing or sealing ceremony at all.
- The **activation factor is the QTSP's**, reached through the seam:
  - `AuthMode: oauth2code` — the signer authenticates directly at the QTSP; we receive a redirect back and finish the authorization.
  - `AuthMode: explicit` — the QTSP issues an OTP/PIN out of band; we relay the factor the signer supplies. We never hold the signing key or mint the factor.
- **No `transaction_data` in our OpenID4VP request in v1.** Carrying document digests + labels in the OpenID4VP authorization request is precisely the wallet-as-factor primitive (the wallet consent *becomes* the activation event). Since v1 does not make the wallet the factor, that primitive is deferred with it. The document-bound consent that legally matters in v1 is the one the **QTSP's SAM** renders as part of the SCAL2 binding.
- **`eudiholder` is not in the QES authorization path.** `internal/eudiholder` holds the *organisation's received* credentials (an org-held wallet). A natural person's qualified signing credential is not an org-held attestation, and QES sole control is the person's own act at the QTSP — so the QES path never touches the holder engine. (A seal credential is likewise machine-held at the QTSP, not in `eudiholder`; see §3.)

The one reusable primitive worth building even in the QTSP-owned model — a **document-bound confirmation screen in our own UI** before we hand off to the QTSP — is a UX affordance, not the activation event, and belongs to the ceremony-UX fog, not this decision. It is called out here only so #177/#178 and the UX ticket know it is *not* load-bearing for sole control in v1.

## 3. QES — a natural person signs "on behalf of the organisation"

**Decision: the sole control of a QES is the natural person's own; the organisational dimension is purely RBAC.**

- **Sole control is the signer's**, exercised at the QTSP (SCAL2, `explicit` or `oauth2code`). We orchestrate; the person authenticates directly to the QTSP. We never hold or relay their key material.
- **`signing:sign` is the org mandate gate.** It authorises *access to the org's signing feature* for that member; it does not weaken the personal sole control. "On behalf of the org" in v1 = an authorised member signing with their **personal qualified credential**. Whether that certificate *also* carries organisational attributes (an org-attributed person cert vs a plain person cert) is **#178's** decision, not this one.
- **The `SubjectRef` invariant (restated from #175, because sole control is what makes it non-negotiable):** for `Kind: signature`, `SubjectRef.SubjectID` is derived from the authenticated session user — never a request parameter. No permission and no request body can make user A authorize a signature under user B's credential. A signature is a person's act, so the actor and the subject are the same identity by construction.
- **Identity-to-certificate matching is deferred to #178.** In v1 we rely on the QTSP's own authentication of the signer in the `authorize` leg; we do **not** cross-check the QTSP credential's subject against our OpenID4VP-disclosed identity. But we **record both** the session user id and the QTSP credential subject in the evidence record (§6), so any divergence is auditable after the fact and #178 has a concrete hook to tighten.

## 4. QESeal — the organisation seals, with no natural-person will

A seal carries no natural-person will at signing time. The seam models it as `Kind: seal`, typically `SCAL1` + `AuthMode: implicit` — a machine-held key the QTSP grants **immediately, with no interactive leg**. There is deliberately no OTP/push/human factor at seal time, because a seal is the organisation acting and organisations need it automatable (bulk-sealing invoices, statements).

**Decision: because the QTSP grants a SCAL1 seal implicitly, the runtime sole-control substitute on our side is *authorization + evidence*, not a human factor.**

- A seal may only be triggered by an org member holding **`signing:seal`** — an administrative-mandate capability, **not** granted to ordinary members by default.
- **The authorization of each seal *is*** the permissioned request plus the append-only `authorization` evidence record (§6) naming the acting user, the org, the credential, and the exact DTBS/R. That record is what proves "the organisation, via an authorised representative, sealed *these* bytes."
- **The legitimacy of the org holding a seal credential at all** — which natural person was mandated to obtain the org seal from the QTSP — is established **once at onboarding/provisioning** by the QTSP. That is **#178's** identity-binding call; it is not re-verified per seal.
- **No per-seal human factor in v1.** If a QTSP later ships a **SCAL2 seal** (eIDAS-2 nudges that way), the seam already absorbs it as `Kind: seal` + `SCAL2` + `explicit` with **no code change**, because callers branch on `Authorization.Status`, never on `Kind`.

**Why a seal is permission-gated but a signature is not:** the subject of a signature *is* the actor (the person), so it needs no separate mandate — the person is authorising their own act. The subject of a seal is the *organisation*, a different principal from the acting human, so the act of one standing in for the other is exactly what `signing:seal` mandates.

## 5. Batch (bounded bulk) and session freshness

**Decision: support bounded batch at the model level; add no app-level step-up beyond the QTSP factor.**

- SCAL2 binds *specific* hashes at authorization time, so one `AuthorizationRequest` may bind **1..N digests, capped at `Credential.MultiSign`**, and the resulting SAD authorizes exactly that set. This is standard bounded bulk signing (a QTSP's mechanism for sealing a batch of invoices), and it costs nothing extra given the #175 seam. Building a dedicated *bulk UX* is not promised here — the ceremony flow is map fog — but the authorization model will not have to change to add it.
- This is distinct from the **multi-*party* signing-package** model (N signers, ordering, routing) still in the map's fog: batch is one signer / one authorization / N documents; a package is N signers.
- **No additional app-level step-up in v1.** The QTSP's SCAL2 activation factor *is* the per-signature sole-control event and is sufficient. A live business-wallet session can *initiate* a ceremony but cannot *complete* a QES without the person's own QTSP factor (the backstop); for a seal, the §4 RBAC gate + evidence is the control. Layering a second re-disclosure on top would be redundant friction. (The session cookie is `HttpOnly` + `SameSite=Lax`, and initiating is a first-party POST, so the initiate action itself is already CSRF-defended.)
- **Authorization freshness** is enforced by `Authorization.ExpiresAt` / `signing_requests.authorization_expires_at` and the `expired` terminal state from #175 — a stale authorization cannot be used to sign.

## 6. The sole-control audit trail

Two distinct surfaces, mirroring `qerds`:

### 6.1 `signing_evidence` — the QTSP's tamper-evident evidence (append-only)

The `authorization` evidence record (from #175's `Evidence{Type: "authorization"}`) is the sole-control proof. It captures exactly what lets an auditor reconcile "the QTSP authorized *these* bytes for *this* credential":

- `provider_ref` — the QTSP's handle for the authorization
- `credential_id` **and the credential subject** (the QTSP-asserted signer/organisation)
- the **digest set** — `DigestID`s + hashes + labels bound in the authorization
- the **acting session user id** (our side of the act)
- the granted-at **qualified timestamp**
- `Raw` — the QTSP's own opaque grant blob (e.g. the CSC `authorize` response)

It **never** contains the **SAD**. The SAD is request-scoped, unexported, redacted under `String`/`LogValue`, and never persisted (#175). We cannot reproduce the SAM's internal binding check — that happens inside the QTSP's HSM — so our record is the QTSP's *attestation* that the binding happened, plus our record of exactly what we submitted. The two together are the sole-control audit trail.

The `signature`, `timestamp` and `revocation-info` evidence records (also from #175) are the format ladder's material (#177), not sole-control, and are unchanged by this decision.

### 6.2 `audit` slice — our who-did-what transaction log

Five actions, recorded through the existing `audit` recorder with i18n (per the repo's audit conventions — each needs an `auditLog.actions.*` translation and an `audit-event.ts` case, or `audit-event.test.ts` fails):

- `signing.authorized` — an authorization was granted (the sole-control event)
- `signing.signed` — a QES was produced
- `signing.sealed` — a QESeal was produced
- `signing.rejected` — the signer declined or a factor failed terminally
- `signing.failed` — the ceremony failed for a provider/technical reason

These give the Art 5(1)(m) transaction-log entries.

### 6.3 Data-minimisation: signing actions stay out of the notifications catalogue

Following the `membership.accept_rejected` precedent, signing audit **metadata is the DTBS/R** — document labels and hashes, potentially the content of a confidential contract. A notification channel is an outside system (SMTP relay, Slack, Teams) that would republish that metadata verbatim. So the five `signing.*` actions are **recorded in `audit` but deliberately kept out of the `notifications` catalogue in v1**. Any later decision to notify on signing events must first decide what non-sensitive summary is safe to emit, and belongs to the sign-and-deliver fog, not this ticket.

## Decisions settled (against the three questions the ticket asked)

1. **QTSP-owned vs wallet-as-factor.** QTSP-owned for v1; we orchestrate the CSC `authorize` flow; wallet-as-factor is a deferred driver behind the same seam (§1).
2. **How the model links to session / OpenID4VP identity and the `eudiholder` engine.** Session only authenticates + RBAC-gates the requesting user; the QTSP owns the factor via the seam's `AuthMode`; no `transaction_data` in our OpenID4VP in v1; `eudiholder` is not in the QES path (§2, §3).
3. **How a seal is authorised and by whom.** `signing:seal` RBAC gate + the `authorization` evidence record *is* the authorization; SCAL1/implicit, no per-seal human factor; the org's mandate to hold a seal cert is established once at onboarding (#178); SCAL2-seal-ready via the existing seam (§4).

## Deferred (boundaries named so the next tickets don't rediscover them)

- **Identity-to-certificate binding** — #178. This ticket records both the session user and the QTSP credential subject in evidence but does not *match* them; #178 decides the person-cert vs org-attributed-cert path and how a verified identity binds to a credential.
- **Document/signature format** — #177. Sole control is format-agnostic; the DTBS/R the SAM binds is a hash, whatever the final AdES format.
- **Signing-ceremony UX**, including the optional our-side document-bound confirmation screen (§2) — map fog. Not load-bearing for sole control in v1.
- **Multi-party signing packages** (N signers, ordering, routing) — map fog. Distinct from §5's single-signer batch.
- **Notifying on signing events** — deferred with the sign-and-deliver fog; needs a data-minimisation decision first (§6.3).
- **RBAC vocabulary.** `signing:sign` and `signing:seal` are named here as the gates the model needs; the resource/action catalogue itself is owned by the RBAC model doc referenced from #175 (`signing` resource: `read`, `sign`, `seal`, `manage_credentials`).

## Verification

Doc-only; no build or test surface is affected. This decision is consistent with the locked seam (`.ai/plans/signing-provider-seam.md`): it adds no method and no type to `signingprovider`, relies only on fields the seam already carries (`SCAL`, `AuthMode`, `MultiSign`, `Authorization.Status`, `Evidence{Type: "authorization"}`, the request-scoped `Activation`), and the five `signing.*` audit actions land in the existing `audit` slice, not the seam.

## Harvest

- Convention to add or update in `.ai/conventions/<area>.md`? **none** — this instantiates the existing external-provider-seam and audit/notifications conventions rather than adding to them.
- Feature doc to write or update in `.ai/features/<name>.md`? **`.ai/features/signing.md`** — but written by the map's capstone ticket, which folds #175–#179 into one architecture doc. This plan is the interim source of truth for the sole-control model.
