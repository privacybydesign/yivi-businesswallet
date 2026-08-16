# Feature: Export (data portability bundle, Art 5(1)(l))

**Status:** Contract and plumbing. `internal/export` assembles the ZIP, the manifest
and the audit event behind `GET /orgs/{slug}/export`. No section writer is registered
yet, so a bundle today carries a conformant manifest with no sections and any
`?sections=` is refused `section_unavailable` — the export says what it cannot do
rather than shipping empty sections that would read as "the org holds none". This file
is the contract the export series builds to: #119 (this doc), #120 (`internal/export`
service + admin-gated endpoint, landed), #121 (owner identification data), #122 (EAAs,
issued + held), #123 (QERDS logs + evidence), #124 (interaction / audit records), #125
(service-termination trigger), #126 (frontend request + download), #127 (async job +
large bundles). Parent issue: #30.
**Regulation:** COM(2025) 838 Art 5(1)(l) (export owner identification data, EAAs,
communication logs and interaction records in a structured, commonly used,
machine-readable format, on owner request or on termination of service / revocation of
the provider's notification), Art 7 (provider-side portability on termination),
Art 8(3) (owner identification data in a format listed in Annex II of Commission
Implementing Regulation (EU) 2024/2979). The Annex requires at least an open format.
**Source refs:** `regulation/COM_2025_838_act.md`, `regulation/FEATURE_LIST.md`,
`.ai/features/attestations.md` §10/§12, `.ai/features/qerds.md`,
`.ai/features/wallet-bootstrap.md`.
**Standards leaned on:** SD-JWT VC and ISO/IEC 18013-5 mdoc (EUDI ARF v2.x), ETSI
EN 319 522 (ERDS evidence), ETSI EN 319 162 (ASiC-E containers), ZIP, RFC 3339.

---

## 0. What is required, and what is ours

Almost everything below is design, not requirement. The law is short, and reading this
file as if it were regulation is how an unforced choice becomes an invariant nobody
questions — which already happened once, to §2's presence rule.

**Required.** Art 5(1)(l), in full: export the owner's data, *including* owner
identification data, EAAs, communication logs and interaction records, "in a
structured, commonly used and machine-readable format", on owner request or on
termination of service / revocation of the provider's notification. Annex §10 adds:
"at least an open format", and the purpose — enabling the owner to migrate to another
Business Wallet solution at assurance level "substantial" or higher. Art 8(3) points
owner identification data at Annex II of Reg (EU) 2024/2979. That is the entire
external constraint.

**Not yet decided anywhere.** Art 5(5) says the Commission will pin reference standards
and specifications for the core functionalities by implementing act. None exists for
export. When one lands it may contradict choices here, and that is a MAJOR bump (§4),
not a defect.

**Ours.** The ZIP container, `manifest.json` and everything in it, the four section
keys and their directory names, per-file checksums, `schemaVersion`, the presence rule
(§2), the `?sections=` filter (§3.1), the omission mechanics (§6) and the secrets
exclusions (§7). These follow from the Annex's migration purpose and from what the data
actually is, but nothing outside this repo asks for them. Change them when they stop
serving that purpose; they carry no more authority than the argument written beside
them.

## 1. What this is

One org-scoped download that carries everything Art 5(1)(l) names, in formats a
receiving system can read without our code: a ZIP whose first entry is
`manifest.json`, and whose four sections mirror the four data points the regulation
lists. The bundle is *portable*, so it is written for a foreign reader: every file is
listed in the manifest with a checksum, every credential keeps the serialization its
issuer signed, and every raw evidence blob stays byte-identical.

Two properties drive every decision below:

- **Verifiable.** Qualified evidence and signed credentials lose their legal effect if
  re-encoded. The exporter copies bytes; it never normalises, pretty-prints or
  re-serializes credential or evidence material.
- **Honest.** A bundle that silently drops a 2 GB attachment is worse than one that
  says it dropped it. Anything referenced but not carried is recorded in the manifest
  with a reason (§6).

The same stance as the rest of the codebase: we orchestrate and package, we are not
the trust service (`qerds.md` §1, `attestations.md` §2). Packaging evidence as ASiC-E
does not make us the sealer of that evidence.

## 2. Container and layout

Top level is a **ZIP**. Deflate is fine (lossless); stored entries are also fine.
Paths are relative, forward-slashed, no leading `/`, no `..`, ASCII only.

The **qualified-evidence subtree is packaged as ASiC-E** (ETSI EN 319 162,
`application/vnd.etsi.asic-e+zip`): one container per QERDS message that has at least
one evidence record, holding that message's raw evidence blobs plus the JSON evidence
index, so evidence and its qualified timestamps travel bound together instead of as
loose files. The ASiC-E is a *packaging* profile here. We add no signature of our own;
the qualified timestamps inside are the QTSP's.

```
manifest.json
owner-identification/
  organization.json          org profile (Art 8 identity root)
  departments.json
  members.json               active + invited entries, no invitation tokens
  credentials/<ref>.sdjwt    owner-ID credentials, native serialization
attestations/
  issued.json                the Art 5(1)(m) issuance ledger
  held.json                  held index, incl. sourceMessageId cross-link
  schemas.json               referenced schema definitions
  templates.json             referenced templates
  keys.json                  key references only, never key material
  credentials/<ref>.sdjwt    held credential material, native serialization
qerds/
  messages.json              metadata + status timeline
  addresses.json
  contacts.json
  attachments/<messageId>/<attachmentId>
  evidence/<messageId>.asice ASiC-E: raw evidence + qualified timestamps
audit-records/
  events.json                or events.ndjson (§5)
```

Section keys, directories and their sources:

| Section key | Directory | Sources | Filled by |
|---|---|---|---|
| `ownerIdentification` | `owner-identification/` | `organization.Store.GetBySlug`, `ListDepartments`, `ListMemberEntries` | #121 |
| `attestations` | `attestations/` | `attestation.Store.ListIssued`, `ListHeld`, `ListSchemas`, `ListTemplates`, `ListKeys`, plus the holder engine for credential bytes | #122 |
| `qerds` | `qerds/` | `qerds.Store.List`, `GetWithEvidence`, `GetAttachmentContent`, `ListAddresses`, `ListContacts` | #123 |
| `auditRecords` | `audit-records/` | `audit.Reader.ListForOrganization` (cursor loop; limits clamp to 200) | #124 |

**A section appears in the manifest only when the export produced it.** Presence means
"we looked"; zero counts then mean the org holds none of that data. Absence means we
did not look — the caller narrowed the export (`?sections=`, §3.1), or this producer
cannot write that section yet. `schemaVersion` is what tells a consumer which keys a
complete bundle of this version contains, so a missing key is never ambiguous about
what the org holds.

The two ways to be absent are both refused rather than silently produced: an unknown
key is a 400, and a known key with no writer registered is a 400 naming it (§3.1). A
bundle never ships an empty section as a stand-in, because an empty section is a claim.

Two cross-section rules keep the same bytes from being written twice:

- `qerds/messages.json` references each ASiC-E container by path. The per-message
  evidence records (type, provider ref, qualified timestamp) live in the container's
  own index, so a container still makes sense after someone lifts it out of the bundle.
- An owner-ID credential the org holds (today the KVK registration attestation) is
  written once, under `owner-identification/credentials/`. `attestations/held.json`
  still lists its held-index row and points at that same path, deduplicated by
  `credential_ref`; #121 owns the file, #122 owns the reference.

## 3. `manifest.json`

The index a consumer reads first: what produced the bundle, for which organization,
when, what is inside each section, and the digest of every file.

```jsonc
{
  "schemaVersion": "1.0",                  // see §4
  "bundleId": "8f14e45f-ea0f-4e2c-9d1b-2b7a0f9c3d51",
  "generatedAt": "2026-07-27T09:14:02Z",   // RFC 3339, UTC, second precision
  "producer": { "name": "yivi-businesswallet", "version": "0.1.0+9a8bb1b" },
  "organization": {                        // Art 8 owner identity, denormalised
    "id": "3d1f0c8e-5b77-4a41-9f0e-0a2c6c2ad4b1",
    "name": "Caesar Groep B.V.",           // the register's official legal name
    "slug": "caesar",
    "kvkNumber": "12345678",
    "euid": "NLKVK.12345678",
    "digitalAddress": "qerds:nl:caesar",
    "status": "active",
    "bootstrappedAt": "2026-01-08T11:22:31Z"
  },
  "sections": [
    {
      "key": "ownerIdentification",
      "counts": { "departments": 3, "members": 12, "credentials": 1 },
      "files": [
        {
          "path": "owner-identification/organization.json",
          "mediaType": "application/json",
          "sizeBytes": 812,
          "checksum": { "algorithm": "sha-256", "value": "9f2b…" }
        },
        {
          "path": "owner-identification/credentials/8c1d….sdjwt",
          "mediaType": "application/dc+sd-jwt",
          "sizeBytes": 2104,
          "checksum": { "algorithm": "sha-256", "value": "41ae…" }
        }
      ],
      "omitted": []
    },
    {
      "key": "qerds",
      "counts": { "messages": 41, "evidence": 96, "attachments": 12, "addresses": 1, "contacts": 7 },
      "files": [
        {
          "path": "qerds/evidence/0b9c….asice",
          "mediaType": "application/vnd.etsi.asic-e+zip",
          "sizeBytes": 18422,
          "checksum": { "algorithm": "sha-256", "value": "7c33…" }
        }
      ],
      "omitted": [
        {
          "path": "qerds/attachments/0b9c…/a7f2…",
          "reason": "size_limit",
          "sizeBytes": 734003200,
          "checksum": { "algorithm": "sha-256", "value": "e0b1…" }
        }
      ]
    }
  ]
}
```

Field rules:

| Field | Type | Rule |
|---|---|---|
| `schemaVersion` | string | `MAJOR.MINOR`, both integers. Required, first key. §4 |
| `bundleId` | uuid | One per export run; also the `targetId` of the `organization.export_requested` audit event (#120), so a bundle can be traced to who asked for it |
| `generatedAt` | string | RFC 3339 with a `Z` offset, second precision. UTC always, never a local offset |
| `producer.name` | string | Fixed `yivi-businesswallet` |
| `producer.version` | string | Build version. Diagnostic only, never a compatibility signal |
| `organization` | object | Denormalised `organization.Organization` (minus `logoUri`). Duplicated in `owner-identification/organization.json`; the manifest copy lets a reader identify the bundle without unpacking it |
| `sections[]` | array | The sections this export produced, in the fixed order `ownerIdentification`, `attestations`, `qerds`, `auditRecords`. A section the export did not produce is absent, not empty (§2) |
| `sections[].counts` | object | Per-collection row counts as exported, after exclusions. Keys are per section and additive. All-zero means the org holds none |
| `sections[].files[]` | array | Every file the section wrote. Sorted by `path` |
| `files[].checksum` | object | `{algorithm, value}`; `sha-256` with a lowercase hex digest over the extracted (uncompressed) bytes |
| `sections[].omitted[]` | array | Payloads a section record references but the bundle does not carry (§6). `reason` is `size_limit` or `unavailable`; `checksum`/`sizeBytes` come from the stored integrity metadata when known, else omitted |

Determinism: sections are in fixed order and files are path-sorted, so two exports of
unchanged data differ only in `bundleId`, `generatedAt` and `producer.version`.

The manifest does not list itself, so it cannot carry its own digest. Bundle-level
integrity (a digest over the whole ZIP, handed to the client alongside the download)
belongs to the delivery path in #120/#127, not to this file.

### 3.1 Section filter

`GET /orgs/{slug}/export` exports all four sections. `?sections=` narrows it to a
comma-separated subset (`?sections=attestations`), which is what an admin who wants
their credential ledger without every member's personal data and the whole audit trail
asks for. There is no second route and no second response shape: a filtered bundle is
the same ZIP with the same manifest, fewer writers run. An unknown key is a 400 —
silently returning an empty bundle for a typo would be indistinguishable from an org
that holds nothing.

A narrowed export is visible in the output by what is absent, not by a flag: only the
sections that ran appear (§2), so a receiver reading `"messages": 0` knows the export
looked and found none. The audit event records the requested set, because exporting one
section is a different act from exporting everything the organisation holds about its
members.

A key this deployment cannot write yet is refused the same way an unknown key is —
`section_unavailable`, naming the section. Section writers are registered as they land,
so before a section is built it is neither offered nor silently shipped empty.

## 4. Bundle versioning

`schemaVersion` is a two-part `MAJOR.MINOR` string on the bundle as a whole. There is
no per-section or per-file version: one number governs the whole layout.

- **MINOR** is additive and backwards compatible: a new section key, a new file in a
  section, a new object field, a new `counts` key, a new credential format. A consumer
  written for `1.0` must keep working against `1.7`.
- **MAJOR** is anything else: removing or renaming a field or section key, changing a
  field's type or meaning, moving a directory, changing checksum semantics.
- A consumer must **reject** a bundle whose MAJOR it does not know, and must **ignore**
  unknown keys within a MAJOR it knows. Both halves of that rule are what makes MINOR
  bumps free.
- The version is decided by the bundle layout, not by the app version. A backend
  release that changes nothing in the bundle leaves `schemaVersion` alone.

| Version | Change |
|---|---|
| `1.0` | Initial contract: ZIP + `manifest.json`, four sections, SD-JWT VC credential material, ASiC-E qualified-evidence subtree |

## 5. Format profile per data point

The proposed default from #119, unchanged:

| Data point | Qualified/native form | Commonly-used export format | Standard basis |
|---|---|---|---|
| Owner ID data | SD-JWT VC and/or ISO/IEC 18013-5 mdoc | Credential in its issued serialization (SD-JWT VC compact / mdoc CBOR) + a JSON profile record | Art 8(3) → Annex II Reg (EU) 2024/2979; EUDI ARF |
| EAAs (issued + held) | SD-JWT VC (mandatory), ISO mdoc (mandatory), W3C VCDM 2.0 (optional, non-qualified only) | Native credential per item + JSON ledger index; W3C VC as JSON-LD | EUDI ARF v2.x |
| QERDS communication logs | message metadata + status timeline | JSON message log; bodies/attachments as files | — |
| QERDS evidence | ERDS/REM evidence (raw bytes + qualified timestamps) | Preserve raw evidence **verbatim** (ETSI EN 319 522-3 XML); bind evidence + timestamps in an ASiC-E container | ETSI EN 319 522/532; ASiC-E EN 319 162 |
| Interaction/audit records | `audit_events` rows | JSON default; NDJSON for streaming; optional CSV | GDPR Art 20 common practice |
| Bundle container | — | ZIP + top-level `manifest.json` (versioned index + checksums); qualified-evidence subtree as ASiC-E | ZIP; ASiC-E EN 319 162 |

Every credential entry in a JSON index carries an explicit `format` token plus the
`path` of its native file, so a reader never infers the format from the extension:

```jsonc
{ "credentialRef": "8c1d…", "vct": "nl.kvk.registration",
  "format": "dc+sd-jwt",                                   // openid4vpverifier/dcql.go
  "path": "attestations/credentials/8c1d….sdjwt",
  "checksum": { "algorithm": "sha-256", "value": "41ae…" } }
```

Format tokens and their files: `dc+sd-jwt` → `.sdjwt` (compact serialization, one
line, no key-binding JWT), `mso_mdoc` → `.mdoc` (CBOR), `ldp_vc` → `.jsonld`. Only
the first exists today.

### 5.1 What the code produces today

The profile is the target contract; the v1 exporter can only emit part of it, and the
gap is in the credential rows:

- The holder engine stores every credential as `models.CredentialFormatSdJwtVc` with
  `RawToken` = the raw SD-JWT VC (`internal/eudiholder/engine.go`,
  `eudiholder.Credential`). No mdoc and no W3C VCDM credential is issued, held or
  received anywhere in the repo; `attestations.md` §14 lists ISO-mdoc as explicitly
  out of v1. So v1 exports `dc+sd-jwt` credentials plus the JSON profile record, and
  the mdoc / VCDM rows stay as the contract for when those formats land. Adding one is
  a MINOR bump: a new `format` token and a new file extension, no layout change.
- `eudiholder.Holder` exposes `Claims` and `Displays` (decoded, localized views) but no
  read of the raw credential bytes. Exporting native serialization therefore needs a
  new holder read (a `Raw(ctx, orgID, ref)`-shaped method returning
  `IssuedCredentialInstance.RawCredential`) rather than a re-encode from `Claims`,
  which would break the issuer's signature. That seam is #122's first task.
- The issued ledger (`attestation.Issued`) holds attribute *values*, not a signed
  credential: the recipient's wallet holds the signed EAA, we hold the ledger row. The
  `attestations/issued.json` index is therefore JSON only, with no credential file.
  Only the held side has credential material to carry.

Audit records default to `audit-records/events.json` (a single JSON array). #124 may
switch to `events.ndjson` for a large trail; both are valid `1.x`, and the manifest's
`path` + `mediaType` (`application/x-ndjson`) is what tells a consumer which it got.
CSV stays optional and is not written by default.

## 6. Inline versus reference for large binaries

The rule, in order of precedence:

1. **No bytes inside JSON.** No base64, ever. A JSON record references a file in the
   bundle by relative `path`, with `mediaType`, `sizeBytes` and `checksum`. This keeps
   the JSON streamable and keeps signed material byte-exact.
2. **`Evidence.Raw` is always carried, verbatim.** The bytes go into the message's
   ASiC-E container unchanged: not truncated, not re-encoded, not re-indented, no
   size threshold. Raw ERDS evidence is what gives a message legal effect
   (`qerds.md` §2), so dropping it to save space is not a trade we make. Evidence is
   bounded in practice (XML receipts, not payloads).
3. **Attachment bytes are carried as files**, one per attachment, read through
   `qerds.Store.GetAttachmentContent`. The on-disk name is the attachment **uuid with
   no extension**; the original `filename`, `contentType`, `contentHash` and
   `sizeBytes` live in the JSON record. Naming by uuid removes the whole path-traversal
   and duplicate-name class instead of sanitising a provider-supplied string.
4. **Held credential material is carried as files** under the section's `credentials/`
   directory, named by `credential_ref` (§5).
5. **Over the bundle budget, a payload becomes a reference-only record.** The
   synchronous export stays synchronous: when carrying a payload would push the bundle
   past its byte budget, the exporter still writes the full JSON record and lists the
   payload under the section's `omitted[]` with `reason: "size_limit"`, keeping its
   stored hash and size so the receiver can still verify it if fetched another way. The
   budget value and its config name are #127's decision; #120 should implement the
   `omitted[]` mechanics with an effectively unlimited budget so #127 only sets a
   number.
6. **A payload the store cannot return is also reference-only**, with
   `reason: "unavailable"`. One missing attachment never fails an export: the manifest
   and the audit event tell the truth about what shipped.

`Message.Body` is text on the message row and stays inline in `messages.json`; only
attachment payloads become files.

## 7. Secrets exclusion

The bundle carries the owner's data, not the means to act as the owner. Deny by
default: a field that grants access, authenticates, or decrypts is out, even when it
is technically the org's own data. Excluding these does not reduce portability, since
none of them mean anything outside this deployment.

| Excluded | Where | Why |
|---|---|---|
| `Invitation.Token` | `organization.Invitation` (already `json:"-"`) | Bearer token: anyone holding it can accept a membership invitation |
| `claim_token`, `offer_uri`, `tx_code`, `IssuanceID` | `issued_attestations` (`attestation.Issued.IssuanceID` is `json:"-"`) | The claim link and its PIN let a bearer claim an unclaimed credential; `IssuanceID` is a live handle on the hosted issuer. An offer is an in-flight handshake, not portable data |
| Session material | session rows, the `ybw_session` cookie | Authentication state, not owner data |
| SMTP password | `email_settings`, encrypted at rest under `EMAIL_ENCRYPTION_KEY` | A credential for a third-party relay. Never exported, not even as ciphertext. `email.Settings` already models this with `HasPassword` |
| Holder key material | irmago's `HolderBindingKey` / `ECDSAKeyMetadata` in the per-org `holder_<orghex>` schema (`attestations.md` §6.5) | Private keys. The credential exports; the key that presents it does not |
| WSCA activation secret | `internal/wsca`, sealed under the deployment KEK (`wsca-holder-binding.md`) | The knowledge factor the WSCA requires on every sign. Per-org, but still a signing credential. The non-secret `wsca.Account` view is the only exportable part, and no section asks for it in `1.0` |
| Every env-sourced secret | `ATTESTATION_ISSUER_ADMIN_TOKEN`, `ATTESTATION_HOLDER_MASTER_KEY`, `EMAIL_ENCRYPTION_KEY`, `DATABASE_URL`, … | Deployment configuration is not owner data |

Exported deliberately, because they are references or public material, not secrets:
`attestation_keys.provider_ref` (a handle into the hosted issuer or QTSP key store),
`certificate_pem` (a public certificate chain), `wallet_unit_attestation`, and
`qerds.*.ProviderRef` (needed to correlate a message with the QTSP's own records).
Private key material is not in the database at all (`attestations.md` §6.4) and must
never be fetched from the issuer or QTSP for an export.

Enforcement, so this survives contact with new fields:

- Each section writer declares its own export record types and copies fields
  explicitly. Never `json.Marshal` a store struct straight into the bundle: a
  `json:"-"` tag is one edit away from disappearing, and the next field added to
  `Issued` would ship silently.
- `internal/export` carries a test that seeds an org with a known invitation token, a
  known claim token and a known SMTP password, exports it, and asserts none of those
  literal values appears anywhere in the bundle bytes, manifest included. A byte-level
  assertion catches a leak through a nested JSONB `metadata` envelope that a
  field-by-field test would miss.
- Org settings (SMTP, theme, issuer instance) are out of the bundle's scope for `1.0`.
  Art 5(1)(l) names identification data, EAAs, communication logs and interaction
  records; deployment configuration is none of those. If a later version adds them, the
  password stays excluded.

The bundle contains members' personal data (names, e-mail addresses, roles), so the
download is admin-gated and audited (#120), and the audit event names the admin who
requested it.

## 8. Open questions

1. ~~**Per-slice route.**~~ **Resolved in #120: folded.** `attestations.md` §10's
   `GET .../attestations/export` does not survive as its own route;
   `GET /api/v1/orgs/{slug}/export?sections=attestations` (§3.1) is the section-scoped
   download. One route, one format, one set of writers — a second route would grow its
   own response shape and drift.
2. ~~**Version gate for new formats.**~~ **Resolved in #120: ship SD-JWT-only.** `1.0`
   carries `dc+sd-jwt` credentials; the mdoc and W3C VCDM rows in §5 stay as the
   contract for when those formats land, and adding one is a MINOR bump (§4). Nothing
   in the repo issues, holds or receives either format today (§5.1).
3. **Sealing the bundle.** Art 5(1)(l) does not require a seal, but a qualified seal
   over `manifest.json` (Art 5(1)(d), the key material in `attestations.md` §7) would
   let a receiving wallet verify provenance rather than trust the transport. Out of
   scope for `1.0`; needs a qualified certificate first (#28).
4. **Annex II conformance for owner ID data.** Today the org's owner-ID credential is
   the KVK registration attestation as SD-JWT VC (`wallet-bootstrap.md`). Whether that
   satisfies Art 8(3)'s Annex II reference, or whether an mdoc profile is required
   alongside it, is a regulatory reading we have not confirmed.
5. **Termination bundles.** #125 exports on service termination or provider
   deregistration, when the requesting admin may no longer be able to log in. Delivery
   for that case (QERDS to the org's digital address, an operator-run job) is #125's
   to settle; the bundle format is the same.

## Harvest

- Convention to add or update: none. The export slice follows the existing
  with-service slice template (`.ai/conventions/BACKEND.md`).
- Feature docs: **this file**, referenced from `.ai/features/attestations.md` §10/§12.
  Cross-link from `qerds.md` when #123 lands (the ASiC-E evidence subtree) and promote
  the status line from Design as the series merges.
