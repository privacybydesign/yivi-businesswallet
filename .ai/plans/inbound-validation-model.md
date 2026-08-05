# Inbound signature-validation model

Status: **proposed** (design only — no production code). Issue [#179](https://github.com/privacybydesign/yivi-businesswallet/issues/179), a grilling ticket under the qualified signing & sealing design map [#171](https://github.com/privacybydesign/yivi-businesswallet/issues/171).

Feeds on closed research [#173](https://github.com/privacybydesign/yivi-businesswallet/issues/173) (AdES formats + Go-vs-DSS tooling), and inherits the DSS-service decision locked in [`.ai/plans/ades-format-dss-boundary.md`](./ades-format-dss-boundary.md) (#177). It locks *how the wallet validates third-party signed documents it receives* — the trust-list source, the validation checks, the report we surface, and build-vs-integrate.

## Why this is a decision and not a copy of #177

#177 decided there is **one self-hosted Java EU DSS service** and that inbound validation runs through it. This ticket owns everything #177 deferred: which trust lists that service consults and how they stay current, exactly which AdES checks constitute a verdict, the shape of the report we show a user, and the confirmation that we **integrate** DSS rather than build validation in Go. It is the inbound mirror of #177's outbound augmentation, sharing the same service so the two never drift on what "qualified" means.

## 1. Trust-list source: EU LOTL + national trusted lists, maintained by the DSS service

**Decision: trust anchors come from the EU List of Trusted Lists (LOTL) and the national Trusted Lists it points at, exactly as EU DSS consumes them; the DSS service keeps them current, not us.**

- eIDAS qualification is defined by the **LOTL → national TL** hierarchy: each Member State publishes a signed Trusted List of its QTSPs and their qualified services, and the Commission's **LOTL** signs the index of those lists. A signature is *qualified* only if it chains to a qualified trust anchor **and** the relevant TL asserts qualified status for that service at signing time.
- **EU DSS already implements this** — TL/LOTL loading, signature verification of the lists themselves, scheduled refresh, and qualification determination against them. We adopt that machinery rather than re-implement it. The service refreshes the LOTL/TLs on its own schedule (DSS's `TLValidationJob`), so "kept current" is a property of running the service, not a cron we hand-build.
- We **pin the LOTL signing certificates** (the Commission's published OJ keys) as the root of trust the service is configured with, so a tampered LOTL cannot inject a trust anchor. That pin is configuration of our own service, inside our boundary.

## 2. AdES validation: the checks, and the report shape we surface

**Decision: full AdES validation through DSS (integrity → chain-to-qualified-anchor → timestamp → LTV/revocation → qualification), reduced to a small, honest status the user can act on.**

### 2.1 The checks (all performed by the DSS service)

1. **Signature integrity** — the signature verifies over the signed `ByteRange`/content; the document has not been altered since signing.
2. **Certificate chain to a qualified trust anchor** — the signing certificate chains to a CA on a national TL, and the TL asserts the service was qualified.
3. **Signing time / timestamp** — a qualified timestamp (B-T+) is validated, establishing *when* it was signed, so validity is judged at signing time, not now.
4. **LTV / revocation** — certificate status (OCSP/CRL) at signing time; for B-LT/B-LTA the material embedded in the document's DSS is used, so validation works even if the CA endpoints are gone.
5. **Qualification determination** — the eIDAS Art. 32 conclusion: is this a **QES/QESeal**, a plain **AdES**, or neither, given the chain + TL + format.

### 2.2 The report shape

DSS emits the full **ETSI TS 119 102-2** validation report. We do **not** surface that raw to the user — we derive a small view from it:

- A **top-level indication** using the ETSI vocabulary the report already speaks: **`TOTAL_PASSED` / `INDETERMINATE` / `TOTAL_FAILED`** (mapped in the UI to plain language: *valid* / *cannot be determined* / *invalid*).
- A **qualification label**: **QES / QESeal / AdES / not-qualified**, plus whether it is a signature or a seal.
- The **signer/sealer identity** as asserted by the certificate subject (name; org name + registration for a seal), clearly framed as *asserted by the certificate*, not verified by us beyond the chain.
- The **signing time** (from the validated timestamp) and the **level** (B / B-T / B-LT / B-LTA).
- On anything other than `TOTAL_PASSED`, the **sub-indication + reason** DSS gives (e.g. `NO_CERTIFICATE_CHAIN_FOUND`, `REVOKED`, `OUT_OF_BOUNDS_NO_POE`), so the user learns *why* rather than a bare "invalid".
- The **full ETSI report is retained** (stored/downloadable) as the authoritative artifact, so a determination is auditable and reproducible; the derived view is a lens over it, never a replacement.

An **`INDETERMINATE`** result is surfaced as exactly that — not silently promoted to valid or demoted to invalid — because "we could not obtain revocation data" is a materially different statement to a user than "the signature is forged," and eIDAS validation draws that line deliberately.

## 3. Build vs integrate: integrate DSS — the same service as #177

**Decision: integrate. Inbound validation runs through the same self-hosted Java EU DSS service #177 stood up for outbound augmentation.**

- #173 established that **no Go library** validates against Trusted Lists with qualification status; the credible options only *verify a signature*, not *determine eIDAS qualification*. Building that in Go would mean re-implementing DSS's TL machinery — the exact thing #177 declined to do.
- So `internal/signing` (or a sibling `internal/signingvalidation` slice) calls the **augmentation seam's `Validate`** counterpart on the same DSS service (`Augmenter.Validate(ctx, document) (Report, error)` alongside the `Augment` from #177). One service, two directions: it augments the documents we produce and validates the documents we receive, and both reach the *same* trust-list state, so our "qualified" verdict is identical whichever way a document flows.
- **Document exposure** is the same posture as #177: validation needs the whole received document, and the DSS service is **inside our trust boundary**, so the document is not sent to any third party to validate it.
- **Dev/CI** uses the same honest stub as #177: it can check signature integrity cheaply but reports qualification as *indeterminate* (no TL loaded), never faking a qualified verdict — the same "a green stub is plumbing, not compliance" caveat the whole map carries.

## Decisions settled (against the three the ticket asked for)

1. **Trust-list source + currency.** EU LOTL → national Trusted Lists, DSS-style, with the LOTL signing certs pinned as our root of trust; the DSS service refreshes them (§1).
2. **AdES validation + report shape.** Integrity → qualified-chain → timestamp → LTV → Art. 32 qualification, all in DSS; surfaced as a small derived view (indication + qualification + asserted identity + signing time + level + reason on failure), with the full ETSI TS 119 102-2 report retained as the authoritative artifact and `INDETERMINATE` shown honestly (§2).
3. **Build vs integrate.** Integrate — the **same** self-hosted Java EU DSS service as #177, called through the augmentation seam's `Validate`; no pure-Go qualification stack; the document stays inside our boundary (§3).

## Deferred (boundaries named for the capstone / execution)

- **The `Validate` interface's concrete wire shape and the derived-report DTO** — execution, once the map's decisions land; this ticket fixes the checks and the surfaced fields, not the JSON.
- **Where inbound validation is triggered** (a received-document view, a `qerds` inbound hook, an upload-to-check screen) — ceremony/UX + sign-and-deliver fog on the map.
- **Long-term re-validation** of documents we hold (re-checking as TLs evolve) — an operational concern layered on the same service later.

## Verification

Doc-only; no build or test surface is affected. Consistent with #177 — it introduces no new external dependency (the DSS service already exists per #177) and no change to the #175 signing seam. The only new surface is the `Validate` counterpart on the already-decided augmentation seam.

## Harvest

- Convention to add or update in `.ai/conventions/<area>.md`? **none.**
- Feature doc to write or update in `.ai/features/<name>.md`? **`.ai/features/signing.md`** — written by the map's capstone ticket, which folds #175–#179 into one architecture doc. This plan is the interim source of truth for the inbound-validation decision.
