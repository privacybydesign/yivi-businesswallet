# AdES format & level decision, and the DSS-service boundary

Status: **proposed** (design only — no production code). Issue [#177](https://github.com/privacybydesign/yivi-businesswallet/issues/177), a grilling ticket under the qualified signing & sealing design map [#171](https://github.com/privacybydesign/yivi-businesswallet/issues/171).

Feeds on closed research [#173](https://github.com/privacybydesign/yivi-businesswallet/issues/173) (AdES formats + Go tooling) and [#172](https://github.com/privacybydesign/yivi-businesswallet/issues/172) (CSC + NL QTSP landscape), and sits on top of the locked seam in [`.ai/plans/signing-provider-seam.md`](./signing-provider-seam.md) (#175) and the sole-control model in [`.ai/plans/sole-control-authorization.md`](./sole-control-authorization.md) (#176). It locks *which signature/document formats and levels v1 produces, and where the cryptographic boundary sits*. It leaves *inbound* validation's own policy to [#179](https://github.com/privacybydesign/yivi-businesswallet/issues/179) (which shares the DSS dependency decided here) and identity-to-certificate binding to [#178](https://github.com/privacybydesign/yivi-businesswallet/issues/178).

## Why this is a decision and not a copy of the research

#173 recommends "lead with PAdES, target B-LT, design a signing-service seam." This ticket turns that into a locked shape: it names the one v1 format, fixes the floor level and the delivered-baseline level, and — the load-bearing part — decides **where the DSS boundary sits and who holds the document**, because that is what determines whether the "the document never leaves our trust boundary" property the #175 signing seam fought for survives the long-term-validation step.

## 1. Formats: PAdES only for v1

**Decision: v1 produces PAdES, and only PAdES.**

A business document-signing wallet's primary artefact is a human-readable PDF — a contract, invoice or order confirmation the counterparty opens, reads, prints and archives as a single file. PAdES is the only ETSI AdES format that embeds the signature *inside* the PDF (incremental update + `ByteRange`), so any standard viewer (Adobe, browser) displays and verifies it without side-car files.

- **XAdES** (XML, for structured e-invoices) and **JAdES** (JSON/JWS — a natural fit for this OpenID4VP/JSON codebase) are **deferred** until signing that data type is a real product requirement. The #175 seam is already format-agnostic — it carries a hash plus a raw signature value, never a document — so adding XAdES/JAdES later is a new builder in `internal/signing`, **not** a seam change.
- **CAdES** is **not** a product target on its own. It is the CMS `SignedData` (RFC 5652) machinery that PAdES embeds; we build it regardless, but never expose it as a delivered format in v1.

## 2. Levels: B-T floor, B-LT delivered baseline, B-LTA where archival is required

The four ETSI baseline levels are cumulative — each is the previous plus more material:

- **B** — signature + signed attributes + signing certificate. Verifiable only while the signing certificate is valid; no proof of *when* it was signed.
- **B-T** — adds a **qualified timestamp** (QTST from a QTSP, eIDAS Art. 42) over the signature, anchoring it to a moment before the certificate later expires or is revoked.
- **B-LT** — adds the full certificate chain + revocation data (OCSP/CRL) embedded in the PDF's **DSS (Document Security Store)**, so a verifier years later needs no live CA/OCSP endpoint. **Self-contained validation.**
- **B-LTA** — adds renewable **archive timestamps** over the whole thing, so the proof survives certificate expiry *and* cryptographic aging.

**Decision:**

- **B-T is the floor.** It is producible in **pure Go** in-process (`digitorus/pdfsign` + `digitorus/timestamp` + `digitorus/pkcs7`), always available in CI and offline, and is exactly what the #175 `StubProvider` loop produces. Anything that must merely prove *when* it was signed is satisfiable without any external service.
- **B-LT is the target delivered baseline.** A signed business document must remain verifiable long after the ceremony, without a verifier reaching *our* endpoints or the CA's, so the long-term-validation material has to travel inside the document. This is the level we aim to deliver for real signatures/seals — but it is **not** pure-Go reachable (see §3).
- **B-LTA where long archival is required** — same augmentation path as B-LT, one step further; treated as a capability of the augmentation seam, not a separate format.

## 3. The DSS-service boundary — two seams, and the document stays inside our trust boundary

This is the crux the ticket names. #173 established that **no Go library** robustly builds the DSS store (embedded certs + OCSP/CRL), applies/renews archive timestamps, or validates against EU Trusted Lists with qualification status. The EU reference implementation, **DSS (`esig/dss`)**, is Java-only and has no Go port. So B-LT/B-LTA + qualified validation force a service boundary — the only question is *where*.

**Decision: split the work across two independent seams, and put long-term augmentation behind a self-hosted DSS service so the document never leaves our trust boundary.**

### 3.1 Two seams, not one

1. **The signing seam** — the existing #175 `signingprovider.Provider`. DTBS/R **hash** in, **raw signature value** out (the deferred/external-signing pattern: the QTSP's key signs one hash and returns bytes, never a finished document). `internal/signing`, using `digitorus/pdfsign`, owns all PAdES construction — the PDF incremental update, the `ByteRange`, the CMS `SignedData` assembly, embedding the signature into the `/Contents` placeholder, and fetching the qualified timestamp (via the seam's `Timestamp` method) to reach **B-T**. **Unchanged by this ticket.**
2. **A new augmentation seam** — `internal/signing` depends on a second, consumer-defined interface (sketch: `Augmenter.Augment(ctx, signedPAdES, targetLevel) (augmentedPAdES, error)` plus a `Validate` used by #179). It takes an **already-signed PAdES B-T** and returns **B-LT / B-LTA**, embedding the DSS store and archive timestamps. It is a **separate seam from the signing provider** so the format decision never reaches back into #175's locked interface, and so the two can be configured, stubbed and scaled independently. It follows the same external-provider-seam convention (config-swappable, stub default, its own health probe).

### 3.2 The document *does* cross the augmentation seam — so the driver must be inside our boundary

The signing seam deliberately never sees document bytes (only a hash). **Augmentation is different: building the DSS store and archive-timestamping require the whole PDF.** So the document bytes *do* cross the augmentation seam. That makes *who runs the augmenter* a data-exposure decision, not just a build choice:

- **Default driver: a self-hosted Java EU DSS sidecar** (`esig/dss` behind a thin HTTP service we run). The document goes to a service **inside our own trust boundary**, so B-LT/B-LTA is reached without the full document ever leaving us. This preserves the exact property the signing seam was built around — the QTSP saw only a hash, and now the long-term step doesn't undo that by shipping it the whole contract.
- **Optional per-org driver: the QTSP's own PAdES augmentation/validation API**, where a QTSP offers one. This is offered but **not** the default, precisely because it sends the full document to the QTSP — a new exposure the org must consciously accept. It exists for tenants who prefer one vendor over running a sidecar.
- **Dev/CI driver: a no-op stub** that returns the input B-T unchanged and reports `Capabilities` honestly (no B-LT). A green stub loop must read as "B-T plumbing works", never as "long-term validity achieved" — the same caveat #175's stub carries.

### 3.3 What this means for shipping

B-LT is the *target*, but delivering it depends on the augmentation sidecar being deployed. Without it, v1 **honestly** delivers **B-T** and the UI/`Capabilities` must say so — never label a B-T document as long-term-valid. This mirrors #175's `Capabilities().Sealing` honesty: a capability we have not stood up is reported absent, not faked.

### 3.4 #179 shares this boundary

Inbound qualified validation (verifying a *received* signature against EU Trusted Lists) is the same DSS capability in reverse, so **#179 should validate through the same Java EU DSS service**, not a second mechanism. That is #179's decision to make, but this ticket fixes the dependency it inherits: there is one DSS service, and it does both augmentation (outbound) and qualified validation (inbound).

## 4. Client-side hash + embed vs QTSP-returns-signed-document

**Decision: client-side hash + embed. The QTSP never returns a finished document.**

Per #173 §3 and #175: the client (SCA) computes the DTBS/R, the QTSP signs that hash and returns a **raw signature value**, and the client assembles and embeds it into the PAdES object. This is the only shape consistent with the #175 seam (`SignReceipt.Signatures` carries raw signature bytes, not documents) and with keeping the document out of the QTSP's hands. A "QTSP returns the signed PDF" flow is explicitly **not** used, because it would put document handling on the wrong side of the seam and re-expose the document to the QTSP.

## 5. Seals use the same PAdES path

**Decision: a seal on a PDF is PAdES, produced by the same path as a signature.**

Sealing and signing differ only in the credential (`Kind: seal` vs `signature`, per #175) and in *who* authorises it (per #176). The **format path is identical**: DTBS/R hash in, raw signature out, client builds PAdES, same augmentation seam to B-LT/B-LTA. `internal/signing` does not fork on `Kind` for format construction.

One documented seam expectation carries over from #172: a proprietary seal **autosigner** (e.g. Digidentity's non-CSC eSeal endpoint) must still return a **raw signature value** to fit the #175 seam, so that we build the PAdES. If a specific autosigner only ever returns a fully-formed signed document (bypassing our PAdES construction and re-exposing the document), that is a **driver-level exception to handle when integrating that provider** (#178 / driver work), not a reason to fork v1's format path. The seam's contract remains: raw signature in the receipt, PAdES built here.

## Decisions settled (against the four the ticket asked for)

1. **Format(s) + level for v1.** PAdES only; **B-T** floor (pure Go), **B-LT** delivered baseline, **B-LTA** where archival is required (§1, §2).
2. **Client-side hash+embed vs QTSP-returns-document.** Client-side hash + embed; the QTSP returns only a raw signature value (§4).
3. **How the format constrains the seam / what bytes cross.** The **signing** seam carries only a **hash** and a **raw signature** (document never crosses it, #175 unchanged). A **separate augmentation** seam carries the **signed PADES document** (it must, to build the DSS), and its default driver is a **self-hosted DSS sidecar** so those bytes stay inside our boundary (§3).
4. **Do seals share the format path.** Yes — identical PAdES path; only the credential and the authoriser differ (§5).

## Deferred (boundaries named for the next tickets)

- **Inbound signature validation policy** — #179. It inherits *this* ticket's DSS-service decision (validate through the same Java EU DSS service) but owns the accept/reject policy, trust-list handling and UI.
- **Identity-to-certificate binding** — #178. Which certificate (person vs org-attributed) is bound is orthogonal to the format; PAdES embeds whatever chain the signing credential presents.
- **The augmentation seam's concrete interface, and standing up the Java EU DSS sidecar** (its deployment, the Compose/k8s service, its config vars) — execution, once the map's decisions land. This ticket fixes its *shape* and *placement*, not its wire format.
- **XAdES / JAdES builders** — added behind the format-agnostic #175 seam if/when XML e-invoice or JSON signing becomes a requirement.

## Verification

Doc-only; no build or test surface is affected. Consistent with the locked seam (`.ai/plans/signing-provider-seam.md`) — it adds **no** method or type to `signingprovider` and relies only on what the seam already carries (the `Timestamp` method for B-T, `Evidence{Type: "revocation-info"}` and `{Type: "timestamp"}` as the material the augmentation step consumes). The one new component, the augmentation seam + DSS sidecar, is a **separate** package/service, so it does not perturb #175.

## Harvest

- Convention to add or update in `.ai/conventions/<area>.md`? **none** — the augmentation seam instantiates the existing external-provider-seam convention. If the "a seam whose driver must sit inside our trust boundary because the payload is the document, not a hash" distinction recurs, it earns a convention line then.
- Feature doc to write or update in `.ai/features/<name>.md`? **`.ai/features/signing.md`** — written by the map's capstone ticket, which folds #175–#179 into one architecture doc. This plan is the interim source of truth for the format + DSS-boundary decision.
