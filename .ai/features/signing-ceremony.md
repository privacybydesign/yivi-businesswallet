# Qualified-signing ceremony (RP-centric SCA over CSC + OID4VP)

Status: **implemented** (backend + gated frontend). Builds on the CSC provider settings
(`internal/csc`, `.ai/features/qtsp-signing-demo.md`). The business wallet acts as the
**RP-centric Signature Creation Application (SCA)** driving a remote QTSP's CSC API v2 to
create a qualified electronic signature over a PDF (PAdES).

## Who does what

The QTSP reference implementation is **backend-only** and owns the wallet ceremony: on
`/oauth2/authorize` its authorization_server starts an **OID4VP** presentation at a verifier
and renders the QR the wallet scans. We are the **client**: we compute the document hash,
launch the browser at the QTSP's authorize URL, catch the redirect back, exchange the code,
call `signHash`, and assemble the signed PDF. We never talk to the verifier directly and the
QTSP never sees the document — only its hash.

## The two-ceremony model (a hard constraint, not a choice)

PAdES SignedAttributes embed a hash of the **signing certificate** (ESS `signingCertificate`),
so the document hash depends on the cert — which must therefore be known **before** the
hash-bound `authorize`. But fetching the cert (`credentials/info`) needs a token, which needs
a ceremony. So signing is split:

1. **Link credential** (once, `POST /orgs/{slug}/signing/credential/link`): a `scope=service`
   ceremony → `credentials/list` (auto-issues if none) → `credentials/info` → cache the
   credential id + certificate + chain per (org, user) in `signing_credentials`.
2. **Sign a document** (per doc, `POST /orgs/{slug}/signing/requests`): prepare the PDF against
   the cached cert to get the hash → `scope=credential` `authorize` binding that hash (SCAL2)
   → token → `signHash` → embed. One wallet scan per document.

## SCAL2 = the OAuth authorize (no separate `credentials/authorize`)

This reference impl folds the SCAL2 credential authorization into `/oauth2/authorize`
(`scope=credential` + `hashes`/`hashAlgorithmOID`/`credentialID`/`numSignatures`, **PKCE
S256 mandatory**). The resulting access-token **JWT is the SAD** — `signHash` re-validates the
request against its claims. There is no `credentials/authorize` call and no separate SAD.

## PAdES: single parked-goroutine pass (`internal/signing/pades.go`)

The signature is produced remotely *after* a minutes-long wallet ceremony, so it cannot be a
synchronous `crypto.Signer.Sign` callback. Instead `StartSign` runs **one** `digitorus/pdfsign`
pass in a goroutine whose `crypto.Signer.Sign` **publishes the ByteRange digest** (which we bind
into `authorize`) and then **blocks** until the QTSP signature arrives via the callback, at
which point the pass completes and embeds it. Because there is exactly one pdfsign pass, `pkcs7`
stamps the CMS `signingTime` once — so **no library patch/vendor is needed** (an early
alternative, a deterministic-time double-run, would have required patching `pkcs7`). Proven by
`TestPAdESExternalSigningProducesVerifiablePDF` (`ValidSignature=true`). Scope: **PAdES-B/B-T**;
B-LT/B-LTA (validation data, archival timestamp) need a separate DSS augmentation seam.

## Packages & routes

- `internal/signingprovider` (leaf): CSC-v2 + OAuth2 client + a `StubProvider` (local EC key +
  self-signed cert) that makes the whole ceremony testable with no network/wallet. Errors are
  redaction-safe (status code only; never URL/token/body).
- `internal/signing` (org slice): the link + sign flows, an **in-memory session manager**
  (parked goroutine + prepared PDF, bounded by `SessionTTL`), the store (`signing_credentials`,
  `signing_requests`), and the handlers.
- Routes: `GET …/signing/availability` (member-safe: CSC configured&&enabled), `GET …/signing/credential`,
  `POST …/signing/credential/link`, `POST …/signing/requests`, `GET …/signing/requests/{id}`,
  `GET …/signing/requests/{id}/document`; plus the **central** `GET /api/v1/signing/callback`
  (the fixed OAuth redirect target, correlated by an unguessable `state`, no session).
- Audit: `signing.credential_linked` / `.requested` / `.completed` / `.failed` — kept **out** of
  the notifications catalogue (metadata is document-related).
- Frontend: the `/{slug}/signing` page (link + upload → authorize QR handoff → poll → download),
  surfaced as a "Sign documents" sidebar plugin gated on `signing/availability`.

## Constraints & limitations (accepted for this capability)

- **Wallet/verifier:** the QTSP authorize step requests an **mdoc PID** (`eu.europa.ec.eudi.pid.1`)
  via the **public EUDI verifier** (`verifier-backend.eudiw.dev`) — it does **not** reuse our
  Yivi/SD-JWT login wallet. Exercising the ceremony end to end needs an **EUDI reference wallet
  with a dev PID** and the `--profile signer` stack running.
- **In-memory sessions:** an in-flight signing session (goroutine + prepared PDF) lives in memory
  — it does not survive a backend restart and assumes a single API instance. Fine for a demo;
  a durable/multi-instance version would persist the prepared state and reconcile the callback.
- **`DefaultRedirectURI`** is a dev constant (`http://localhost:8080/api/v1/signing/callback`,
  matching the AS client registration); a real deployment behind another host must make it
  configurable.
- Sign algorithm is ECDSA-P256 / SHA-256 only (the reference QTSP's supported set).

## Running it locally (verified end to end against a real EUDI wallet)

1. `docker compose --profile signer up --build` — brings up the app plus the QTSP resource
   server (`:8085`) and authorization server (`:8084`). First run only: if `qtsp-authz` fails
   with "table doesn't exist", `docker compose --profile signer down -v` once and up again (the
   Spring OAuth schema loads only on a fresh `signer-mysql` volume).
2. `.env` needs `CSC_ENCRYPTION_KEY` (`openssl rand -hex 32`) or the org can't save the client
   secret.
3. In **Settings → Signing**, use the sample preset and set:
   - **base URL `http://qtsp-signer:8085`** (NOT `localhost:8085` — the backend calls it
     server-side from its container),
   - client id `yivi-business-wallet`, client secret `yivi-business-wallet-demo-secret`, enable.
4. Install the **EUDI reference wallet**, get a dev **PID**. Add the EUDI verifier's RP trust
   anchor to the wallet if prompted (`certificate_of_issuers/PIDIssuerCA02-EU.cacert.pem` from
   the reference repo). **Yivi does not work here** — it rejects that CA's mdoc EKU and lacks an
   mdoc PID.
5. **Sign documents** page → link the credential (one scan) → upload a PDF → scan → download the
   signed PDF.

### Local-Docker gotchas baked into the compose (each was a real failure while bringing this up)

- **CSC base URL** must be the container name `qtsp-signer:8085`, not `localhost` (backend calls
  it server-side).
- **`VERIFIER-DOMAIN` / `WALLET-SCHEME`** are passed to `qtsp-authz` as **Spring command-line
  args**, not `environment:` — Docker Compose silently drops env var names containing hyphens.
- **OAuth issuer host split**: the browser reaches the AS at `localhost:8084` (the authorize
  URL) but the backend reaches it at `qtsp-authz:8084` (the server-side token exchange), so
  `SIGNING_OAUTH_ISSUER_INTERNAL` overrides only the backend's token-exchange host. Empty in
  production, where the QTSP is one URL for both.
- **`client_id` in the token body**: the reference token endpoint requires `client_id` as a form
  field even with `client_secret_basic`; `signingprovider.ExchangeToken` sends it in both.
- **Authorize `hashes` are base64url**, `signHash` hashes are standard base64 (the token claim
  the AS re-encodes to). Same digest, two encodings — mixing them 500s the AS.

## Verifying a signed PDF

The produced PDF is a detached CMS/PAdES signature (`/Type /Sig`, `/ByteRange`,
`/SubFilter /adbe.pkcs7.detached`). To check one:

- **`pdfsig file.pdf`** (poppler): shows the signer (the PID identity), signing time, SHA-256,
  and "Signature is Valid".
- **Adobe Acrobat** Signatures panel — valid signature, but "signer's identity is unknown".
- **`digitorus/pdfsign/verify`** — the programmatic check `pades_test.go` uses.

**Expected: "certificate issuer is unknown" / "not trusted".** The demo QTSP self-signs the cert
(the EJBCA-free path), so it is not a qualified cert in the EU Trusted Lists / Adobe AATL — this
is the "plumbing, not qualified compliance" line made concrete. A real production QTSP issues a
cert that chains to an EU trust list, and the same signature would validate as trusted/qualified.

### Follow-ups
- The signature subfilter is `adbe.pkcs7.detached` (a widely-accepted CMS detached signature).
  For strict PAdES-baseline badging, switch pdfsign to the `ETSI.CAdES.detached` subfilter.
- `DefaultRedirectURI` and the local-Docker host overrides above are dev conveniences; a real
  deployment points at one publicly-reachable QTSP URL and needs none of them.

See also `.ai/features/qtsp-signing-demo.md` (the hosted QTSP + `--profile signer`, incl. the
authorization_server) and `internal/csc` (per-org provider settings).

## Co-signing (multiple signers + recipient delivery)

The single-signer ceremony above is the building block; a **request** is now an org-level
co-signing object.

- **Model.** `signing_requests` carries `created_by`, the `original_document`, an accumulating
  `signed_document`, a `signing_mode` (`parallel`/`sequential`), a recipient
  (`recipient_channel` none/qerds/email + address/name/message) and a `delivery_status`.
  `signing_request_signers` holds one row per selected member (`sign_order`, per-signer `status`).
  Each member signs with **their own** linked credential — the PK on `signing_credentials` is
  still `(org, user)` — so the finished PDF carries **N qualified signatures**, applied
  incrementally (`pades_multisig_test.go` proves two incremental signatures both verify).
- **Order.** *Parallel* = any order; *sequential* = ascending `sign_order`, and a signer's turn
  is only offered once every lower-order signer has signed (`ListPendingForUser` /
  `Service.checkTurn`). **"Parallel" is not simultaneous:** incremental PAdES appends to a specific
  base document and cannot merge concurrent signatures, so an in-memory **per-request in-flight
  lock** (`Service.acquire`/`release`) serialises the actual signing passes either way.
- **Flow.** `POST /requests` (member) creates the request (validates signers against the member
  directory, stores the PDF); `POST /requests/{id}/sign` (member, must be a pending signer whose
  turn it is) runs *that signer's* ceremony over `GetLatestDocument` (signed ?? original);
  `finishSign` writes the new bytes + marks the signer signed, and when all have signed marks the
  request completed and **delivers** it.
- **Delivery.** On completion the finished PDF goes to the recipient over the chosen channel:
  `email` reuses `email.Service.SendSignedDocument` (a new `signed_document` mail Kind + the new
  `mailer` attachment support — `multipart/mixed`); `qerds` reuses `qerds.Service.Send` with the
  PDF as an attachment from the org's default QERDS address. Delivery is best-effort — a failure
  leaves the request `completed` with `delivery_status=failed`, surfaced in the history, not fatal.
  The `signing` slice stays decoupled via consumer interfaces (`memberDirectory`,
  `documentDeliverer`) implemented by adapters in `cmd/api` (`signing_adapters.go`).
- **UI.** The Sign page is tabbed: **To sign** (documents awaiting me → run my ceremony),
  **New request** (upload + member multi-select + order + recipient), **My credential** (the
  once-off link, moved out of the per-document flow). The admin **History** tab (styled like the
  settings tabs) lists the org's requests, cursor-paginated, with per-signer + delivery status.
- **Signer notifications.** Each selected signer is e-mailed that a document awaits their signature
  (`email` Kind `signature_requested`, linking to the signing page) via the `signerNotifier` seam
  (adapter in `cmd/api`). Parallel mode notifies every signer at create time; sequential mode
  notifies only the first, then the next signer as each turn completes (`finishSign`). Best-effort:
  a mail failure never blocks create or advance.
- **Audit + subscriptions.** Adds `signing.signed` and `signing.delivered` alongside
  `signing.requested`/`.completed`/`.failed`. The lifecycle events `signing.requested` /
  `.completed` / `.failed` are a subscribable **`signing` group** in the notifications catalog
  (`internal/notifications`), so admins can also be notified over their configured channels — safe
  because that metadata is the org's own filename/mode/status, never a disclosed identity.
- **Still in-memory/single-instance:** the active ceremony (parked pass + reservation) lives in
  memory; only the between-signers document is persisted. An abandoned ceremony frees its lock at
  `SessionTTL` without failing the request, and a signer can reclaim their own stale slot at once.

## External signees (a signer who is not an org member)

A signer no longer has to be someone in the organisation. Per signer, the requester picks
**internal member** or **external signee** (name + e-mail), and one request may mix both.
An external signee signs with **their own EUDI wallet**, through the same two ceremonies a
member runs — so the finished PDF carries one incremental PAdES signature per signer
regardless of kind.

- **Model.** Both signing tables lost the "a signer is a row in `users`" assumption
  (`20260810120000_signing_external_signees.sql`). `signing_request_signers` has a surrogate
  `id` primary key and a **nullable** `user_id`, plus `external_email` / `external_name` and
  the *hash* of the signee's invitation token; a `CHECK` keeps a row to exactly one kind, and
  two partial unique indexes keep the old "one row per member per request" guarantee while
  adding "one row per address per request". `signing_credentials` is re-keyed the same way, so
  an external signee's linked credential lives under `(org, lower(external_email))` — the
  `subject` type in the store is what names either kind, and it is deliberately unexported so a
  caller cannot address a credential row without going through a signer.
- **The signer row id is what addresses a signer.** `checkTurn`, `reserveSign`,
  `RecordSignature` and `FailRequest` all key on it rather than on a user id, which is what
  makes the parallel/sequential turn logic and the per-request in-flight lock work unchanged
  for a mixed set of signers. `ListPendingForUser` still joins on `user_id` (a member's "to
  sign" list), and its lower-order check counts external signers too, so a member queues behind
  a pending external signee exactly as behind another member.
- **The way in is a tokenised link, no session.** `IssueExternalToken` mints a 32-byte token
  (only its SHA-256 is stored, same construction as an org invitation) with an
  `ExternalInviteTTL` (30 days) expiry, and it is issued **when the signee is actually asked** —
  at create time in parallel mode, when their turn comes in sequential mode — so a third-in-line
  signee has no live link until it is their turn. The unauthenticated routes
  `GET|POST /api/v1/signing/external/{token}[/document|/credential/link|/sign]` each resolve
  that token to one signer row and read nothing else from the caller (the same posture as
  `GET /invite/{token}`). Signing does **not** delete the token: the ceremony returns the signee
  to that link and it is where they see their signature landed and can re-read what they signed.
  Replay is stopped by their signer status (`ErrAlreadySigned`), and re-linking a credential is
  refused once they have signed, so a stale link cannot swap the certificate that subject would
  sign a later request with.
- **Data minimisation.** `ExternalView` is deliberately narrower than `Request`: the signee is
  outside the organisation, so they get who is asking, the document name, their own status and a
  signed/total count — never the other signers' names or addresses.
- **Notification.** The invitation reuses the existing `signature_requested` mail Kind; only the
  `signingUrl` differs (their `/sign/{token}` page instead of the org's signing page), so there
  is no new mail copy to translate. Best-effort, like the member notification.
- **UI.** The create form's signer picker is an **ordered list** (that order is the sequential
  signing order) fed by a per-signer *internal member / external signee* choice. The external
  signee's own page is the public route `/sign/:token`: review the document, link a certificate
  once, then sign.

## Signature & paraph placement (where a signer's marks land)

Every signature above is **invisible** — a `/Rect [0 0 0 0]` widget, cryptographically complete
and visually absent. A real document usually wants a signature block per signee at a fixed spot
and their initials (a *paraph*) on each page. Per signer, the requester now places both, and the
signing pass renders them.

- **Model.** `signing_signer_placements` (`20260810140000_…`) holds one rectangle per row:
  `(signer_id, kind, page, x, y, width, height)`. `kind` is `signature` (**at most one per
  signer** — a partial unique index says so, because a PAdES signature dictionary carries exactly
  one appearance) or `paraph` (**at most one per page per signer**). "A paraph on every page" is
  expanded by the requester into one row per page, so the table stays flat and nothing downstream
  has to interpret a shorthand. The rectangle is in **PDF user-space points, origin bottom-left** —
  the space pdfsign's appearance rectangle is in, so the conversion from viewer coordinates happens
  exactly once, in the browser, which is the only place that knows the zoom, the page rotation and
  the crop box.
- **Validation, at create time.** `readGeometry` (`placement.go`) replaces the old `validatePDF`:
  it parses the upload, reports every page's visible box (CropBox else MediaBox, inherited through
  the page tree — digitorus/pdf ships that walk commented out) and doubles as the upload check.
  Being the upload check is what bounds it: what it refuses is what cannot be uploaded at all, so
  it refuses a page with **no area** and not merely a small one. Whether a mark fits is a question
  about the mark, and refusing the document would reject it over a page nobody is placing anything
  on. `validatePlacements`
  then refuses a page the document does not have, a rectangle outside the page, one below
  `minPlacementSize` (8 pt), a second signature block or a second paraph on a page. Nothing
  constrains the *sign* of a rectangle, in Go or in the table: a page whose crop box starts below
  the origin puts every rectangle on it in negative coordinates, and containment in the page box
  is the whole rule. A
  placement is **refused, not clamped**: it is where a person pointed at a rendering of *this*
  document, so a value off the page means the two disagree, and quietly moving the signature
  somewhere else is the one outcome nobody asked for. Placement stays optional as a whole — no
  placements means the invisible signature that every signature was before this existed.
- **The signature block is the signature's own appearance.** `signatureAppearance` hands the
  rectangle to pdfsign's `Appearance{Visible, Page, …}`, so the widget *is* the placed rectangle
  and the mark is the signature: it cannot be moved without breaking it
  (`TestSignaturePlacementBecomesTheVisibleAppearance`).
- **Paraphs are a revision the signer's own signature covers.** They cannot be part of that
  appearance — one signature, one appearance stream, one page — so `stamp.go` appends an
  incremental revision of printable, locked `/Stamp` annotations **immediately before** the signer's
  pdfsign pass. Being inside the ByteRange their signature covers is what makes a paraph
  attributable: editing one invalidates that signature
  (`TestStampedParaphIsCoveredByTheSignature`). The annotation's appearance stream matches
  pdfsign's own — Times-Roman, ballpoint blue — so the two marks on a page read as one hand, and
  the text is the initials of the certificate's common name (`paraphText`).
- **The appended section has to match the document's own.** `appendRevision` writes a classic
  cross-reference table for a table-indexed document and a cross-reference **stream** for a
  stream-indexed one (`rdr.XrefInformation.Type`): a table appended to an xref-stream file would
  leave a reader that found it first with no way into the object streams the previous section
  indexes. `TestParaphStampMatchesTheDocumentsXrefKind` covers both, and the page dictionary is
  rebuilt key by key (not copied — digitorus/pdf resolves as it reads and hands back no raw
  object) by `writeValue`, which emits only forms that are valid PDF.
- **`pdf.Value.String()` is a debug formatter and neither library may use it here.** It renders a
  stream as `<<…>>@offset` and a PDF string through `strconv.Quote`, so a page carrying
  `/Metadata`, `/Thumb`, or the `/LastModified` + `/PieceInfo` pair InDesign and Illustrator write
  routinely stops parsing once it has been rewritten through it. `writeValue` avoids it by telling
  a reference from a direct value the only way digitorus/pdf allows — comparing the resolved
  value's `GetPtr()` with its container's — and writing the reference rather than what it points
  at. **pdfsign has the same bug in its own page rewrite** (`createIncPageUpdate`), which runs only
  when `Appearance.Visible` — so nothing reached it until placement existed; it also writes every
  `/Annots` entry as a reference, turning a directly-embedded annotation into a reference to the
  page. Since no form of such an entry survives being resolved and re-formatted, `stampMarks` hands
  pdfsign a page it can round-trip: on the **signature block's page only**, the entries in
  `droppablePageEntries` are dropped and direct annotations are promoted to objects of their own.
  That list is page metadata and nothing else — `/Metadata`, `/Thumb`, `/LastModified` with its
  `/PieceInfo`, `/ID` — none of it drawn on the page or changing what the page does, and the drop
  happens in the revision the signature then covers, the only place a change to a signed document
  may be. **Anything else pdfsign cannot emit is refused rather than dropped**: a page quietly
  stripped of its `/Resources` would come out blank, and the refusal lands in `stampSignerMarks`,
  before the digest is published, so no signature is spent on it. A page carrying just a paraph is
  never handed to pdfsign's rewrite and keeps everything (`stamp_test.go`:
  `TestVisibleSignatureKeepsAnAwkwardPageReadable`, `TestParaphOnlyPageKeepsItsEntries`,
  `TestDirectAnnotationOnTheSignaturePageSurvives`,
  `TestSignaturePageWithAnUndroppableEntryIsRefused`).
- **pdfsign writes every reference in that page at generation 0, and the page object too.** It
  formats `/Parent`, `/Contents` (single and array) and each `/Annots` entry from `GetPtr().GetID()`
  with the generation a literal `0` (`sign/pdfvisualsignature.go`:133, :142, :148, :154), and
  replaces the page as `id 0 obj` (`sign/pdfxref.go`:71). A producer that reuses an object number
  writes the reused object at a higher generation, which is legal PDF — pdfsign's copy of the
  reference then points at nothing, so **the page draws blank, its own annotations vanish, or the
  page disappears from the tree, while the signature still verifies**. Silent content loss inside a
  document whose signature checks out is the worst outcome available, and it is reachable only on
  this path: an invisible signature never rewrites the page, and `stamp.go`'s own revision keeps the
  generation (`pdfRef` preserves it, `incrObject` carries it), so a paraph on the same page is fine.
  `/Parent`, `/Contents` and the page object itself are therefore **refused** — pdfsign rewrites
  that page from whatever it is handed, so there is nothing to normalise — and `allRefsAtGenZero`
  walks arrays because a content array's entries are written out individually. An `/Annots` entry
  **is** fixable and is copied into an object of its own at generation 0, alongside the promotion
  direct annotations already get. `pageNeedsNormalising` has to agree with all of it, or a signature
  block on a page with no paraph never reaches the refusal (`stamp_test.go`:
  `TestSignaturePageWithAReusedObjectNumberIsRefused`,
  `TestReusedAnnotationOnTheSignaturePageSurvives`; `testpdf_test.go`'s `objectGens` builds them).
- **The paraph's font states its encoding.** Times-Roman with no `/Encoding` reads the content
  stream's bytes in Adobe StandardEncoding, where the UTF-8 of `Ünal` draws as a macron plus a
  notdef. The font declares `/WinAnsiEncoding` and `winAnsi` transcodes to it, with `?` for a
  character it has no place for; the box is measured in the bytes that are drawn, not the runes
  they came from. The annotation's `/Contents` is a PDF *text* string, a different encoding again,
  so it is written UTF-16BE (`pdfTextString`) and carries a name in any script.
- **Accepted cost, stated plainly.** A signer with paraphs adds **two** revisions instead of one,
  and the extra one is not itself a signing operation. Earlier co-signatures stay cryptographically
  valid (the revision is appended after their ByteRange, `TestCoSigningWithPlacementsKeepsBothSignaturesValid`),
  but a viewer that lists changes since a signature will name it. Getting a paraph into the *same*
  revision as the signature would mean patching pdfsign, whose `SignContext.addObject` is
  unexported — the one thing this capability has so far avoided. This is also the answer to the
  design question the issue left open: a paraph is **attributable** (inside that signer's signed
  bytes) rather than being a second cryptographic signature of its own.
- **UI.** `routes/signing-placement.tsx` renders the uploaded PDF with pdf.js, one page at a time,
  and overlays each signee's marks colour-coded from the house accents (there is no "signee colour"
  in the design system; every mark also carries the signer's name, so colour is never the only cue).
  Click the page to place or move the selected mark; drag one to fine-tune; **place in the middle of
  this page**, the arrow keys (Shift for larger steps) and the width/height fields are the paths that
  need no dragging at all (WCAG 2.2 §2.5.7). The geometry is pure and unit-tested
  (`lib/placement.ts` + `.test.ts`), and `placement-viewport.test.ts` pins the one assumption the
  whole feature rests on — that a rectangle converted through the pdf.js viewport and back is the
  same rectangle — against a real document at four rotations and two zooms. Paging is the primary
  flow here (a paraph on every page), and pdf.js refuses a second `render()` on a canvas a live
  task still holds, so the render lifecycle is `lib/pdf-page-render.ts`: one task at a time,
  cancelled before the next starts, with a cancelled render reported rather than thrown.
- **pdf.js is a chunk of its own.** It is larger than the rest of the app put together, so the
  editor is a `React.lazy` import: the main bundle is unchanged for everyone who is not placing a
  signature.

### Follow-ups
- A paraph is drawn as initials from the certificate's common name. A hand-drawn or uploaded
  signature image would go in the same rectangle (pdfsign's `Appearance.Image` for the block, an
  image XObject for the paraphs) and is the obvious next step.
- Placement is defined once, by the requester. Letting a signee adjust their own block before
  signing is a separate decision — it would have to be bounded, since the request is what they are
  agreeing to.
- Rotation is handled through the viewport in the browser; nothing on the backend reads `/Rotate`,
  because it never has to convert a coordinate.
