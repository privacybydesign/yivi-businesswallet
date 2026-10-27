# Feature: Identity proofing

**Status:** Integrated (backend + frontend). The wallet runs proofing itself, in
its own engine (`internal/proofingengine`, §0); every org is the engine's
tenant under the org's own id, with no provisioning step. Flows are the org's
main object: an org has one set of flows with one default, one set of settings,
one set of API keys and one webhook endpoint, and no customers (§1). The
sidebar's "Identity proofing" section holds up to three pages under
`/{org}/identity-proofing`: **Overview** (a to-do list: **Waiting for your
decision**, **Waiting for the person**, **Recently finished**, one line with the last 30
days' counts, and an alert for a failing webhook) and, admin-only, **Flows** (a table of the org's flows
with the people each identified in 30 days, the default flow and which flows
members may use; a flow opens to its own page, `/{org}/identity-proofing/flows/{flowId}`,
with **People** and **Versions** tabs, **Edit flow** (tabs **Check**, **Branding**
and **Hosted page**) and "Verify a person") and
**Developers** (no tabs: the **API tokens**
table (name, created with who by, last used, revoke; revoked ones are not
listed), the **Webhook endpoint** and its **Recent deliveries**). The **All people** list
(`/{org}/identity-proofing/people`, opened from the Overview;
`?session=<id>` opens a row) is not in the sidebar; a flow's People tab is the
same table narrowed to that flow by the backend (`?flowId=`), as is All people
when its Flow filter picks one, whose retention line then states that flow's
retention. A count read from a list of 200, the cap, may be short, so it shows
as "200+" ("12+" for a part of it). "Verify a person" in the
header of the Overview, of All people and of a flow opens the send form, for any member. From a
flow the dialog sends on that flow; from the Overview or All people it asks first what to send
(for an admin: an identity check, See my data or Delete my data, §15) and, for an identity
check, the flow. It asks how the person gets the QR code (the e-mail address sits inside the
e-mail option; a flow whose "Allow sending this flow as a link" switch is off offers only Show on
this screen) and has two switches, off by default: "Add the person's name" and "Only let this
person pass". The Flows page lists neither GDPR request: one line under the table says See my
data and Delete my data are always available and links to Settings › Data and privacy. Only a
platform admin pauses an org's proofing, after which no request can be sent
(`proofing_paused`, 403) while sent ones run out (§8). A member is sent a
request from their member detail page (admin-only, like that page). The people
an org verifies are its members, or external people with no login: an e-mail
address and an optional name, a request without a `userId`. The org's own
backend can send to them through the public API with one of its keys. Sending
creates an engine session (2, 5 or 10 minutes: the flow's own lifetime when it
sets one, else the org's fixed 10 minutes for an external person) and mails its vcmrtd deep link as a QR code
and a button: the mail is the session, with no wallet page in between. The
outcome lands on the request, in the audit log and, for an external person, at
the org's webhook.
The send form can instead **show the session on this screen**
(`channel: on_screen`, for a subject who is with the member): it opens
`/{org}/identity-proofing/verify?flow=`, a page in the org's branding that shows what
the flow collects, lets the subject pick the Yivi app or the Idem app, and only
then creates the request (so the session's minutes start there). Idem shows the
vcmrtd QR and link; Yivi starts an **OpenID4VP** disclosure of the passport,
ID card or residence permit at the hosted EUDI verifier, `EUDI_VERIFIER_URL` (`yivi/start`:
`ScopeProofing`, the `openid4vp://` request shown as an `open.yivi.app` universal
link and QR; the transaction id stays in `yivi_transaction_id`), and polls
`yivi/disclosure`, which on DONE hands the claims to the engine
(`SubmitReference`).
- The disclosure decides the session: approved, or rejected
  `document_not_accepted` when the document breaks the flow's restrictions.
- No face check runs: the Yivi app vouches for its holder (the passport issuer
  checks the face when the credential is added), so the method needs no
  Regula and the session is eIDAS low with no tier (`boundLoginAssurance`).
- The photo is asked only when the flow keeps the face photos and releases
  one (`keepsFacePhoto`, `disclosedPhoto`).

A subject
who cancels in the app is not reported: the verifier only knows pending or done,
so the page waits out the countdown. Both count down to the
session's expiry and poll `GET requests/{id}` for the outcome. Nothing is
mailed, and the subject's name and address travel in router state, not the URL.
The hosted page's completion (redirect origins, decline, locale) is in §7.
**Slice:** `internal/proofingengine` (the engine: sessions, flows, the Idem app's
routes, chip verification, results, Regula), `internal/proofingprovider` (the
seam's value types + a test stub, leaf level),
`internal/proofing` (org settings, flow selection, members, requests, service,
handler), `internal/email` (kind `identity_proofing_requested`, the `qr` block),
`frontend/src/routes/member-proofing.tsx` (the member detail panel),
`frontend/src/routes/identity-proofing-overview.tsx` (the overview),
`frontend/src/routes/developers.tsx` + `developers-api-keys.tsx` +
`developers-webhook.tsx` (the Developers page), `proofing-branding.tsx`
(Settings › Branding) + `proofing-branding-fields.tsx` (its fields and mail
preview, shared with a flow's), `flow_branding*.go` +
`routes/flow-branding.tsx` (a flow's own branding), `routes/flow-tabs.tsx`
(the flow page's tabs),
`internal/proofing/api_handler.go` (the
public API and the API key routes), `apikey_store.go` (keys),
`org_settings_store.go` + `settings_handler.go` + `branding_handler.go` (the org's
settings; `settings_store.go` is the flow selection),
`webhook*.go` (endpoint, outbox, deliverer), `internal/safehttp` (the SSRF
guard every externally supplied URL goes through),
`frontend/src/routes/identity-proofing-flows.tsx` (admin: the flows table),
`flow-editor.tsx` + `flow-editor-advanced.tsx` + `flow-editor-parts.tsx` (the
flow editor), `frontend/src/lib/issuing-countries.ts` (its country list),
`identity-proofing-flow.tsx` (a flow's page: People and Versions), `frontend/src/routes/proofing-people.tsx` (the people table and its
details; a row in review links to its review page), `proofing-reviews.tsx` (the
Reviews page), `proofing-review.tsx` + `identity-check-review.tsx` +
`data-request-review.tsx` (the review pages, §9), `proofing-send.tsx` (the Verify a person dialog),
`proofing-verify.tsx` (the on-screen check), `proofing-ui.tsx` (marks and
tags), `proofing-pause.tsx` (the paused notice), `hosted*.go` +
`routes/proof.tsx` (the hosted link, §7), `pause*.go` (§8), `flow_hosted*.go` +
`routes/flow-hosted-settings.tsx` (per-flow hosted settings), `diploma*.go` +
`internal/diploma` + `routes/flow-editor.tsx` (the flow's diploma
step) + `routes/proofing-verify-steps.tsx` (the upload) (DUO diplomas, §12),
`matchSubject` in `service.go` (one known person, §13), `address*.go` +
`routes/proofing-verify-steps.tsx` (`AddressStep`) (the postal address, §16).
**Depends on:** the Regula Face API (`REGULA_FACE_API_URL`) for every face
check, and the Idem app's `/api/v1/app/{token}/...` contract
(`vcmrtd/idem/lib/services/proofing_session_client.dart`).

---

## 0. The engine

`internal/proofingengine` runs the proofing sessions: their state machine, the
flow definitions and versions, chip verification, the result with its BSN and
image redaction, assurance scoring and the Regula face checks. Tenants, API
keys, webhooks, the audit log and the admin pages are the wallet's.
`proofing.Service` drives it through Go methods (`rp.go`: flows, create
session, status/result/identity, review, handover, cancel, delete) with a
`proofingprovider.Tenant{ID: org id}`; nothing goes over HTTP. The Idem app
talks to the routes `Engine.Register` mounts under `/api/v1/app/...` (claim,
view, events, steps, submit, device state, and minting and claiming a handover;
documented under the `idem-app` tag, though only the app calls them), and the deep link's `api=` is
`IDENTITY_PROOFING_PUBLIC_URL` (default `APP_BASE_URL`).

- **Who reaches the app routes.** A Yivi-method session (bound login) runs
  through the wallet, never the app: its token opens no `/app` route (409
  `not_app_session`). The `/events` long polls are capped at 4 per session and
  512 per process (`maxEventWaitersPerSession`, `maxEventWaiters`; past it 429).
  A waiting poll reads the row's write time each tick (`Store.ChangedAt`) and
  unseals the session only when that moved or the deadline passed.
  The request log writes a token path segment as `{token}` (`loggedPath` in
  `internal/server`), for these routes and every other token link.
- **A session needs a flow of its org** (`CreateSession`): none, or one the
  engine does not hold, is refused, since such a session could never finish.
  A method other than the Idem or the Yivi app is 400. The Yivi method is
  refused (400) on a flow it cannot serve: one requiring more than low,
  matching a reference photo, or photographing the document, whatever the
  caller offers.

- **Storage.** Flow versions in `identity_proofing_flow_versions` (definition as
  JSON, one active version per flow); sessions in `identity_proofing_sessions`,
  the session itself (evidence included) sealed under
  `IDENTITY_PROOFING_ENCRYPTION_KEY`, only token and grant hashes in the clear;
  Regula tags to delete in `identity_proofing_regula_sweeps`. Without the key
  every session is refused (`ErrNoEncryptionKey`); flows still work. A sealed
  session opens only in its own row: its sealed id and tenant must be the
  row's, so data copied into another org's row does not read there.
  Concurrent saves of one flow are serialised per flow (an advisory lock), so
  each gets the next version.
- **Changes reach the wallet in process.** The engine calls
  `Service.SessionChanged` (opened, step started, outcome, expiry). A lazy
  expiry is noticed by whichever read finds the deadline past.
- **Faces.** Only Regula: the native face step (liveness transaction + match
  against DG2). The Yivi method runs no face check.
  - A face step that failed liveness or did not match may be submitted again:
    how often is the app's call; the engine bounds it at 3 attempts a session
    (`maxFaceStepAttempts`), after which the last failed evidence stands and
    the submit decides on it. Each attempt's transaction is released as usual.
    - Each submit reserves its attempt under the session lock before Regula
      is called (`reserveFaceAttempt`), so concurrent submits never exceed the
      bound. An attempt that left no evidence because Regula or the engine
      failed is given back (`refundFaceAttempt`); one naming a transaction
      Regula does not know, or another session's, stays spent.
    - With every attempt reserved and none leaving evidence, a submit is 409
      `face_attempts_spent`.
- **Images.** Every uploaded image is decoded through `images.Decode`: its
  header is checked first (4 megapixels, `images.MaxImagePixels`, above the
  app's largest image, a 1600 px document photo; a JPEG2000 1 megapixel, as a
  chip portrait is well under it and JPEG2000 decodes slowly, and at most 4
  colour components), and at most 4 decode at once in the process. A slot
  waited for over 10 s is `ErrBusy`, a decode running over 10 s is
  `ErrDecodeTimeout` (it keeps its slot until it ends), and a decoder panic is
  `ErrDecoderPanic`. A JPEG2000 chip portrait is converted to PNG in pure Go
  (go-jpeg2000, the only pure-Go decoder; OpenJPEG would mean C in-process)
  for display. The BSN region is covered through the same helper.
- **Face photos are not kept by default.** The document portrait (DG2, or a
  disclosed credential's), the selfie and the sender's reference photo
  leave the result and the stored evidence once the face match is done
  (`redactStepsForStorage`, `buildResult`); the match's verdicts stay. A
  session that finishes in review keeps them until it is decided, for the
  reviewer to compare (`heldForReview`); `DecideReview` drops them then
  (`dropFacePhotos`). A flow
  sets `KeepFacePhotos` for an org that must keep them, for its
  retention. Covering a face instead was dropped: a mosaic or blur can still
  be matched against candidate photos, and a flat cover holds nothing. The
  wallet shows kept photos blurred until a member clicks to reveal one.
  Masking only the printed portrait on a document photo (as the KopieID app
  does) is not built: it needs the portrait's position.
- **Regula clean-up.** Every Regula tag the app is handed is queued for
  deletion (`identity_proofing_regula_sweeps`): 15 minutes after the session's
  expiry while it runs, moved forward to 15 minutes after its end once it ends,
  whatever the outcome (`Queue.Settle`). An erased session (`DeleteSession`)
  is due at once. `Engine.SweepRegula` deletes them.
- **Retention.** Every session the wallet creates carries its own retention,
  the one the wallet purges the request after, plus a day (`engineRetention`),
  so `PurgeDue` erases it first: its flow's retention override when set, else
  the org's fixed 7 days for an external person's request, else for a member's
  request `proofedNameRetention` (30 days); for an external person's identity
  check never less than the org's legal retention (§4). A legal retention
  saved later is given to the sessions already created (`syncEngineRetention`,
  §4). The engine's own
  default, 90 days after a session ended, applies only to a session created
  without one. `PurgeDue`, `Engine.Purge` (removal in batches of 100, each with
  its own timeout) and `Engine.SweepRegula` run at start and then every
  `SESSION_PRUNE_INTERVAL` (`startPruner`): a replica restarted more often than
  the interval would otherwise never purge.
- **Device trail.** Which device claimed a session, handovers, and refused app
  requests go to the org's audit log (`Config.DeviceTrail` →
  `RequestStore.RecordDeviceEvent`: `identity_proofing.device_claimed`,
  `.device_handed_over`, `.handover_issued`, `.handover_claim_failed`,
  `.access_denied`, target the request; device ids, roles and reasons only).
  Reconnects and device states stay in the server log.
- **Chip access key.** The MRZ-derived key that opens the chip is handed to a
  device only while the chip read is the current step, and dropped once the
  chip evidence lands, so a device taking the session over later never gets
  it.
- **Change notices** go through one bounded queue (`Engine.Run`), one per
  session while it waits; a full queue drops the notice and the deadline job
  reconciles instead.
- **Claims are rate-limited per session** (`ClaimLimit`, 20 a minute), not per
  client IP: behind the reverse proxy every phone has the proxy's address.
- `IDENTITY_PROOFING_PROVIDER=stub` swaps in `proofingprovider.Stub`, which no
  phone can reach (tests, and `IDENTITY_PROOFING_STUB_OUTCOME` in dev). It and
  the stub outcome run only with `DEV_MODE` (a localhost `APP_BASE_URL`; the dev
  stack sets it), and the stub's approved person is a specimen: issuing state
  and nationality `UTO`.

---

## 1. Model

- **The org is the engine's tenant: one tenant id, the org's.** A session and a
  flow carry `organizations.id`; there is nothing to provision, no key to hold
  and no enable step.
- **There are no customers; what an org sets up is the org's own, once, for all
  its flows.** Every session runs on the org's tenant in the engine, and an
  external person has no login or row of their own: they are the subject of a
  request. The org holds (§4 has the detail):
  - **Settings**, one row per org (`identity_proofing_settings`,
    `OrgSettingsStore` in `org_settings_store.go`, `Settings` in `proofing.go`):
    session (QR) lifetime and data retention, fixed defaults of 10 minutes and
    7 days that each flow overrides (`SessionTTLSeconds` and
    `RetentionOverrideSeconds` on the flow, §2, §10); how "delete my data"
    requests are decided (`review` or `automatic`, default `review`); a legal
    retention; branding (display name, colours, font, logo, support contact,
    privacy URL, hide "powered by"); allowed redirect origins. No row reads as
    the defaults (10 minutes, 7 days, review, no legal retention, the org's own
    look, no redirect origins).
  - **API keys**, one set: a key works on every flow and reaches every
    external request of the org. No key is needed to send from the app.
  - **One webhook endpoint** for the events of all its flows.
  - **Flows**, one set with one default, and the list of flows members may use
    (below). Nothing is assigned to anyone; who may send which flow is under
    Recipients.
  - The rate limits (`apiCallLimit`, `APISessionLimit`) and the hosted page's
    (`hostedCallLimit`) are per org.
- **Flows live in the engine** (`proofingengine/flow`), versioned per org; the
  wallet's "Flows" pages edit them through `proofing.Service`, and it is
  `RequireOrgAdmin`. The rules are `flow.Validate`'s, mirrored in the editor:
  - Steps: `document_capture` (vcmrtd scans the MRZ to unlock the chip) and
    `nfc_read` (NFC chip read) toggle as a pair, shown as one step, "Scan and
    read the document"; `document_photo` (a photo of
    the printed page, `POST /app/{token}/steps/document_photo`) stands
    alone; `face_verification` is one step. `draftSteps` lists
    `document_photo` between `document_capture` and `nfc_read`: the Idem app
    photographs the side it reads the MRZ from as it reads it, cut to the
    frame, and shows it for review (use or retake) right after the scan,
    whatever the listed order: a passport's photo page is then the whole
    photo; a card's MRZ side is its back, so the front is taken next. The engine
    does not enforce the order.
  - Checks: `nfc.passive_auth` is locked on with `nfc_read`, and `face.match`
    with `face_verification`; `nfc.chip_auth` and `face.liveness` are optional;
    a check without its step is unavailable; the threshold is only for
    `face.match` (see the face match threshold below). "Reject expired
    documents" is a read-only row, always on: the engine refuses an expired
    document on every flow (`DOC_EXPIRED`).
  - Requested data: `dg1` comes from the document scan; `dg11`, `dg2` and
    `chip_checks` from NFC; `document_image` from `document_photo`; `selfie`
    and `biometrics` from face. The editor shows plain labels, never these
    codes, in the order of `REQUESTED_ATTRIBUTES`. Each item is available only
    with its step and cleared otherwise;
    none is forced on, and a new flow starts with none ticked; a data request
    or required diplomas tick and lock `dg1` (`identityRequired`). With none
    ticked the editor saves `outcome_only`, which
    releases the outcome only (status and assurance), as the subject is told.
    A flow saved without a list releases everything its steps collect
    (`requestedAttributesOf`), and a flow read back always lists what it
    releases.
  - Age check (`age_over`, "Age check"): releases whether the holder is the
    flow's `AgeOver` (1..120 years) or older, as `ageOver: {age, over}`.
    - The editor lists it under What you receive, after Name and document
      details, and picks the age in a "Minimum age" dropdown under it: 12,
      16, 18 (the default), 21 or 65, plus a saved flow's own age when it is
      another (`ageOverChoices`).
    - Derived by the engine (`ageOverOf`) at the chip read, from the date of
      birth in the verified chip evidence, never the MRZ scan alone:
      `flow.Validate` requires `nfc_read`, and `AgeOver` set exactly with the
      attribute.
    - The date of birth is never stored (`asksAge`); it exists only in memory
      during the scan and chip-read requests.
      - Document scan: the MRZ is stored without it, and without the chip
        access key made from it. The app opens the chip with its own scan; after
        a restart it scans again.
      - Chip read: stores `NFC.AgeOver` and the chip checks (`NFC.ChipChecks`),
        and drops the parsed date, DG1 and DG11 (`withholdBirthDate`).
      - Finish: scores the kept chip checks (`finishChipChecks`), since Passive
        Authentication cannot run again without DG1.
      - `TestAgeCheckStoresNoBirthDate` checks the stored session after each
        step.
      - Yivi app: the disclosure asks the passport or id-card's age claim
        (`over12`, `over16`, `over18`, `over21`, `over65`,
        `openid4vpverifier.AgeClaim`) instead of `dateOfBirth`, and the engine
        answers from it (`disclosedAgeOver`, released as `result.ageOver`).
        - A date of birth the app discloses anyway is dropped before storing
          (`withholdDisclosedBirthDate`). `TestYiviAgeCheckStoresNoBirthDate`
          checks it.
        - A disclosure without the age claim, or with a value other than
          Yes or No, rejects the session `AGE_NOT_DISCLOSED` (code
          `age_not_disclosed`), so an approved age check always answers.
        - Offered only for those ages (`yiviAppAvailable`): a saved flow with
          another age is Idem-only.
        - The claim is set when the credential is issued: a "yes" holds, a
          "no" can be out of date until the credential is renewed.
    - Can be ticked with `dg1`, and then wins: `buildResult` releases the
      document data without `dateOfBirth` (name, nationality and the rest
      stay).
      - Such a flow does not read the identity (`readsIdentity`), so it cannot
        check for one known person, and a data request or diploma flow, which
        needs the date of birth, cannot tick it: choosing either ticks `dg1`
        and unticks the age check (`withIdentity`), which then stays off with
        a lock note, and the editor refuses to save the two together for such
        a flow (`flowDraftError`), as `EditFlow` does (`releasesIdentity`).
      - The subject's consent screen describes `dg1` without the date of
        birth (`dg1WithoutBirthDate`).
    - `over` holds from the birthday the holder turns `age`, on the UTC date
      of the chip read (born 29 February: 1 March in a common year).
    - It decides no outcome: an approved minor is approved with `over: false`;
      the relying party decides.
    - Off in a new flow, never in a flow without a list (`attributesForSteps`).
    - A collected item like any other: it needs its reason in each offered
      language (`withReasons`), the editor asks for it under the item, and
      the person reads it as "Why: ..." under the age check. The CM seed flow
      gives "To check you are 18 or older." and keeps `dg1`'s reason to the
      name on the ticket.
    - Reaches the public API and admin result as `identity.ageOver` (the
      reviewer's read of a check in review too, `newAPIIdentity`), a person's
      row details in the people table ("18 or older: Yes/No"), on a flow's
      People tab and on All people, and an identity check's review page, under
      the nationality (`ageOverFact`). The Idem app's consent screen names the
      age from the app session view's `ageOver`.
  - **Reasons.** Every item a flow lists, other than `outcome_only`, has a
    reason, required in each language the flow's hosted page offers (§7, every
    supported one until an admin sets them), and the person reads it under the
    item as "Why: ...", in the page's language. A version stores them as
    `attributeReasons` (item, then language, at most
    `maxAttributeReasonLength` characters each). `Service.CreateFlow` and
    `EditFlow` check them against the hosted languages (`withReasons`, 400
    `invalid_input`); the engine only stores them, as the languages are the
    wallet's. The editor shows a field per supported language under each
    ticked item: required in the offered languages, optional in the others,
    so a reason can be written before the Hosted page tab offers its language.
    A flow saved before reasons existed keeps working: its page shows no "Why"
    line, and the editor asks for the reasons at the next save. The Hosted page
    tab (`SaveFlowHosted` → `hostedOffersReasons`) refuses to offer a language
    the active version gives an item no reason in, 400 `reasons_missing`, and
    says so beside the languages with a way to the Check tab; a version saved
    before reasons gives none and may offer any language. Activating an older
    version is not checked. A save that lists no items (the engine reads that
    as everything its steps collect) is not checked; the editor always lists
    them. The seeded demo flows give reasons in en and nl for everything they
    collect (`seed.demoReasons`).
  - Countries: `acceptedIssuingCountries` takes 3-letter ICAO 9303 codes only,
    and a code no country has is refused, so the flow cannot be saved
    (`flow.ValidIssuingCountry`: ISO 3166-1 alpha-3, plus ICAO's own EUE, UNO,
    UNA, UNK, XOM, XPO, XCC, XES, XMP, RKS and the GBD/GBN/GBO/GBP/GBS
    variants). A German document writes "D", which counts as DEU
    (`flow.IssuingStateCode`). The editor picks them from a list, never typed
    (`lib/issuing-countries.ts`: the ISO countries named in the page's language
    and ICAO's own codes named by the editor), and a Go test beside
    `ValidIssuingCountry` holds every code on it to the engine's. "Any country"
    saves an empty list.
  - Document types: `acceptedDocumentTypes` lists document codes or kinds
    (`AcceptsDocumentType` matches either). The editor saves kinds, as the
    engine names them: Passport (`passport`), Identity card
    (`identity_card`), Residence permit (`residence_permit`) and Dutch driving
    licence (`drivers_license`); `lib/identity-proofing.test.ts` holds them to
    `flow.go`. The engine tells a chip's kind from its code, issuing state and
    nationality (`DocumentKindOf`), so a residence permit coded `ID` is no
    identity card and a `PD` passport is a passport; a Yivi credential's kind
    is its credential (`yiviDocumentKinds`), as the disclosure carries no code
    (`openid4vpverifier.proofingQuery`), so a flow listing codes refuses every
    Yivi disclosure and one listing kinds does not. A stored code reads as its
    kind (`P`; `I`, `ID`; a kind in any case), and an unchanged save sends the
    stored list as it was; another code is kept. All four ticked saves an
    empty list, any document: `DocumentKindOf` leaves some cards without a
    kind (a foreigner's card coded `ID` is a residence permit in Belgium, an
    identity card in Estonia), which a list of all four would refuse.
  - **Whose identity:** the engine reads the document (name, number, dates,
    nationality, DG11's full name, BSN and place of birth) off the chip
    evidence itself, `documentFromEvidence` in `proofingengine/chipdocument.go`:
    a passport's or ID card's DG1 MRZ and DG11 through gmrtd, a driving
    licence's ISO/IEC 18013-2 DG1. The `document` the app posts on
    `/steps/nfc` is ignored: nothing binds it to the chip, so a modified app
    could pair a genuine chip with another person's name. A DG1 that does not
    parse is a 400.
  - **Driving licences: Dutch only.** The document type is `drivers_license`
    (`document.type`, `chipAccess.documentType`, `acceptedDocumentTypes`); the
    chip evidence says `eu_driving_licence`, which `flow.Validate` refuses in
    `acceptedDocumentTypes`, since no document carries it.
    - Trust anchor: the RDW's `CSCA NL eDL` roots, generations 01–03, never
      in this repo; as go-passport-issuer does it, ops mounts them and the
      backend reads them at startup.
      - `IDENTITY_PROOFING_EDL_CSCA_PATHS`: comma-separated files, PEM or
        DER, into `mrtdverify.LoadCertPool`. Unset trusts no
        licence (`CSCATrustChainValid` false, so `DOC_TAMPERED`).
      - Source: `yivi-businesswallet-ops` `certificates/edl/gen{1,2,3}.cer`
        (copied from go-passport-issuer-ops), a ConfigMap mounted at
        `/certificates/edl`; ops builds the env var from the files, so a new
        generation is one more file.
      - Local: `compose.override.yaml` mounts `EDL_CSCA_DIR` at the same
        `/certificates/edl`.
        - Default: the empty, committed `dev-setup/certificates/edl`, so
          Docker never creates a root-owned directory outside the repo.
        - Real CSCAs: set `EDL_CSCA_DIR` to an ops checkout's
          `certificates/edl`, plus `IDENTITY_PROOFING_EDL_CSCA_PATHS`.
      - Tests: `mrtdtestfixtures.NewLicenceChip` signs a licence EF.SOD per
        call under a fresh test CSCA (no RDW test vector exists);
        `TestDrivingLicenceChip` runs it through `nfc_read` and submit.
    - Passive Authentication: `mrtdverify.VerifyPassiveDrivingLicence`, as
      go-passport-issuer's `PassiveAuthenticationEDL`: DG1 required, and a DG13
      the EF.SOD lists but was not sent is `DOC_TAMPERED`.
    - Active Authentication: DG13 holds the key (method
      `active_authentication`); `resolveAAKeyDataGroup` defaults to it when sent.
    - Portrait: DG6, so `photoFromChip` and the face match use it.
    - Another state's licence is a 422 `document_unsupported`
      (`drivingLicenceSupported`): at `document_capture` by its issuing state or
      chip access country, at `nfc_read` by the chip's own DG1. Without its
      CSCA a genuine one could only end `DOC_TAMPERED`.
  - **Assurance level**: none, low or substantial; high is not offered (no
    certified anti-spoofing). `flow.LevelRequirements` (mirrored in the
    editor's `LEVEL_REQUIREMENTS`) says what each needs:
    - low: the chip read, `nfc.passive_auth` verified.
    - substantial: low plus `nfc.chip_auth`, `face.match` and `face.liveness`,
      the face matched by Regula against the chip's portrait (DG2, a
      licence's DG6).
  - **Picking a level ticks what it needs and locks it** (`withAssuranceLevel`,
    `lockedByLevel`): Low the chip read and its genuineness check, Substantial
    also the copy check and the face check with liveness. The rest stays free,
    and what a higher level ticked stays ticked when a lower one is picked.
    `draftFromFlow` applies the level too, so a stored version is consistent
    with its locks. A flow whose settings still cannot reach the level is not
    saved (`reachesAssuranceLevel` in the editor, `flow.Validate` in the
    engine).
  - **The level a session achieves** is the highest whose checks all verified
    on the evidence it produced (`computeEIDASAssuranceLevel`):
    - computed without the required level, so it can come out higher;
    - only Regula lifts a face past low;
    - a check that did not apply or run (no AA key, AA not attempted, no
      liveness result) has not verified: low at most.
  - **`requiredChecks` are what the steps perform** and the score counts. They
    change one outcome: with `nfc.chip_auth` listed, a chip with an AA key that
    sends no AA response is a clone (below). The Idem app follows them, never
    its own settings:
    Active Authentication only when `nfc.chip_auth` is listed; Regula liveness
    always; the face engine the flow names.
  - **What rejects a session** (`sessionOutcome` in `submit.go`), first match
    wins:

    | Cause | `errorCode` | Audit `reason` |
    |---|---|---|
    | Tampered or cloned chip | `DOC_TAMPERED`, `CHIP_CLONE_DETECTED` | `tamper_detected` |
    | Outside the flow: document type, country, expiry, face step without server evidence (`flowComplianceFailure`) | `DOC_EXPIRED`, … | `flow_policy_violation` |
    | Face step did not verify the person (`faceStepFailure`) | `FACE_NO_MATCH`, `LIVENESS_FAILED` | `check_failed` |
    | Achieved level below the flow's (`flow.MeetsLevel`, fails closed) | `ASSURANCE_NOT_MET` | `assurance_not_met` |

    - **Clone** (`authenticityFailure`):
      - a failed Active Authentication, on any flow;
      - with `nfc.chip_auth` listed and an AA key on the chip, a missing
        response too: a recorded chip read replayed without the chip;
      - without `nfc.chip_auth` the app runs no AA, so a missing response
        only keeps the session from substantial;
      - leaving out a DG15 (a licence's DG13) the EF.SOD lists is
        `DOC_TAMPERED` (`DocumentComplete`).
    - **Face step:**
      - `FACE_NO_MATCH` gates at any level;
      - `LIVENESS_FAILED` gates at any level: the app runs Regula liveness
        always, whether or not the flow lists `face.liveness`;
      - a face that failed liveness never counts as matched, whatever its
        score, and never reaches substantial.
    - `CHIP_AUTH_MISSING` appears only on sessions decided under an earlier
      rule.
  - **The wallet re-checks the level** against the one the request stored at
    send (`required_assurance_level`, `enforceAssurance`): an approval below it
    becomes `rejected` with `ASSURANCE_NOT_MET`. A Yivi-method session is
    approved by the engine without comparing (create already refused it on
    a flow above low); one reported with no level counts as low
    (`yiviEIDASLevel`).
  - Privacy and timing, always saved explicitly by the editor:
    - BSN policy, "Leave it out" (`omit`, a new flow's), "Keep the last 3
      digits" (`mask`) or "Keep the full BSN" (`retrieve`). The engine reads a
      flow without a policy as `omit` (`privacy.DefaultBSNPolicy`), so the
      editor shows a stored flow with none as that (`UNSET_BSN_POLICY`): an
      organisation may process a BSN only where a law allows it, so keeping
      one is the flow's choice.
    - "Black out the BSN on document photos" is `blurBsn` (on in a new flow),
      which fills the printed BSN region solid on a Dutch document's photos;
      another country's are released as taken.
    - "Keep face photos after the match" is `keepFacePhotos` (off in a new
      flow): off pixelates the chip portrait and the selfie once the match
      ran.
    - Retention in days ("Organisation default (7 days)", 1, 30 or 90; stored
      in seconds, and a stored value that is none of them stays a choice),
      which replaces the org's fixed 7 days for the flow's sessions.
    - The session (QR) lifetime (the org's fixed 10 minutes, 2, 5 or 10
      minutes), saved on the flow version.
  - The face match threshold is a slider under Checks while the flow has the
    face check: 0 to 1.00 in steps of 0.01, the value beside it, a scale at 0,
    0.50, 0.75 and 1.00. A flow with none starts the slider at 0.75, Regula's
    default (`FACE_MATCH_RECOMMENDED`), and saves one only once it is moved.
    The flow's threshold replaces the deployment's
    (`REGULA_FACE_MATCH_THRESHOLD`) for that flow's face match, lower or
    higher, at the Idem app's face step (`faceMatchThreshold`; the Yivi app
    runs no face check); a flow
    requiring Substantial matches at 0.75 at least, also on a deployment set
    lower, so a face under it fails the face step rather than reaching the
    end and failing `ASSURANCE_NOT_MET`. Before, a flow's threshold could only
    make the deployment's stricter: migration `20261021116000` dropped every
    stored one under 0.75, from inactive versions too, so an existing flow
    matches as it did (on a deployment set under 0.75, one stored between the
    two is lost). At Substantial the range starts at 0.75
    (`SUBSTANTIAL_FACE_MATCH_FLOOR`, `flow.SubstantialMinFaceMatch`): the part
    below is greyed out and out of reach, picking Substantial lifts a lower
    stored threshold to it, and the engine refuses a Substantial flow with one
    under it. At None or Low the whole range can be picked, with a mark at
    0.75; below it the track and value turn amber and a warning with a tooltip
    appears. The eIDAS level a session reaches has the same floor
    (`meetsFaceMatchFloor`): a face scored under 0.75 reaches no more than Low,
    whichever threshold matched it, the flow's or the deployment's.
  - Legal basis, purpose and assurance tiers are not in the editor: they are
    carried over unchanged when a version is saved.
  Face capture is forced to `native` (§11). The face provider is always
  `regula` (anything else is 400): the engine has no face engine of its own.
  The subject picks the Yivi app or the Idem app on the verify page
  (`yiviAppAvailable`, the same name in the backend and the frontend), unless the flow
  photographs the document or matches against a reference photo, which only
  the Idem app does, or requires more than low, the level a Yivi session
  reaches (`yiviEIDASLevel`; it would only end `ASSURANCE_NOT_MET`). A face step without `nfc_read` needs a
  per-session reference photo (§14): such a flow is not `completable` for
  members, but it is `externalCompletable`, so an admin or an API key may send it
  to an external person (only the API carries the photo). A flow with
  `document_photo` (front and back, or a passport's photo page) runs in the
  Idem app only: it rules out the Yivi app. "Edit" is
  `POST /flows/{id}/versions`: the new version is active at once, and a session
  pins the version active when it is created, so sent requests keep theirs
  (`flow_version`). Activating an earlier version rolls back. Audited:
  `flow_created`, `flow_version_created`, `flow_version_activated`, each with
  the full configuration (a flow holds no personal data).
- **Flow selection.** `identity_proofing_flow_settings.member_allowed` marks the
  flows an admin made available to members, and `is_default` exactly one
  default (partial unique index), the org's: the flow an external person's
  request without a `flowId` runs, and the one the send form preselects. The default is
  independent of `member_allowed`; it must be an identity flow an external person
  can be sent (`externalCompletable`), never a data request flow (a CHECK, and
  400 `invalid_input` from `validSelection`). That table is the wallet's one row per org and
  flow id, shared with the hosted, diploma, address and kind settings below; saving one
  leaves the others alone, and no row reads as every default. `GET .../flows` (and
  the API's `GET /proofing/flows`) never lists a data request flow (§15), and its rows
  carry `hostedEnabled`, the flow's "Allow sending this flow as a link" switch.
  `PUT /flow-selection {flowIds, defaultFlowId}` replaces the selection whole
  (`flowIds` are the flows members may use), audited `identity_proofing.flows_configured`
  with before/after. `GET .../flows` rows carry `allowed`, `default` and
  `retentionDays`. A new flow starts unselected. A request to a member is
  refused (`flow_not_allowed`) on any flow outside the list, for admins too
  (for an external person, see Recipients). An id the
  engine does not list is ignored on read. With the stub provider, flows live in memory, so
  a backend restart empties the list while the settings rows stay (and are ignored).
  Stub sessions never decide unless `IDENTITY_PROOFING_STUB_OUTCOME` is set
  (`approved`, `rejected` or `needs_review`; `DEV_MODE` only); then the stub reports the change
  (`SessionChanged`) 2 s after creation, as the engine would.
- **Language.** A request carries the sender's wallet language (`language`,
  `en`/`nl`, sent by the frontend from its i18n state; optional on the public
  API). It is the request mail's locale and the engine session's `language`,
  which the Idem app gets, so the app shows the same language. Absent, the mail
  uses the deployment default and the app the phone's language.
- **Recipients: a member, or an external person.** A request names either a
  `userId` of the org (`subject_user_id`, `subject_kind` `member`; any role,
  employees and externals alike), or an e-mail address and an optional name
  (`subject_kind` `external`, no `userId`; a CHECK keeps an external request
  free of one). `subject_kind` is stored because `subject_user_id` is cleared
  when the member's user is removed; `SubjectKind` and `Request.External()` read
  it, and the request responses carry `external`. An external person is never a
  user or member. Name and address are a snapshot; `subject_name` is `''` when
  none was given. Which flow:
  - a member recipient: only a flow members may use (`flow_not_allowed`
    otherwise), whoever sends;
  - an external person: an admin and an API key may send any flow a recipient
    can finish (`externalCompletable`); a plain member only a flow members may
    use (`flow_not_allowed` otherwise);
  - an omitted `flowId` is the org's default flow for an external person, else
    422 `no_default_flow` (`sendableFlow` in `service.go`; `Requester.Admin`
    and `APIKeyID` decide who is held to the members' list).
- **Roles:** any member lists the flows made available to them, reads the org's
  settings, sends requests, and sees the requests they sent; creating and
  editing flows, versions and the selection, changing the org's settings, API
  keys and webhook, reading a request's result and deciding reviews are
  `RequireOrgAdmin`; an admin lists every flow (with `allowed`/`default`) and
  every request of the org. `GET requests?subjectUserId=` narrows either view to
  the requests proofing one member, `?subjectKind=external|member` to those
  sent to external people or to members, and `?flowId=` to those sent on one
  flow (any version; index `identity_proofing_requests_org_flow_idx`); the
  narrowings combine, and the list's cap of 200 applies after them, so the All
  people page and the Overview ask for `external`, and a flow's People tab for
  its `flowId` too.
- **Customers were removed from the model** (thirteen migrations,
  `20261021100000` to `20261021101200`, after the newest one that touched
  customers, `20261019120000`;
  `internal/migrate/customers_removed_integration_test.go` runs them over
  several customers' data, down and up again). What the org keeps of them:
  - their settings, merged (`identity_proofing_settings`, migration
    `20261021100000`): what the customers agree on stays; where they differ,
    no customer's subjects get less than that customer promised them, so the
    shortest session lifetime and data retention, erasure handling `review`,
    and every redirect origin any of them allowed (so each one's portal still
    embeds the hosted page). The look (display name, colours, font, logo,
    support contact, privacy URL, "powered by") cannot be merged and is the
    oldest customer's;
  - all API keys, now the org's. A key keeps working, except that a paused
    customer's keys are revoked (a pause stopped them, and nothing pauses a
    customer any more). A key reaches every external session of the org (list,
    read results, cancel, purge), also those sent from the app or for a
    customer that never had a key, and the org's one webhook gets every flow's
    events. So in an org with more than one customer, a key now reaches
    sessions of customers it was not issued to. The migration does not rotate
    keys; before deploying, find the orgs where that happens (a working key and
    more than one customer) with

    ```sql
    SELECT c.organization_id
    FROM identity_proofing_customers c
    GROUP BY c.organization_id
    HAVING count(*) > 1
       AND EXISTS (SELECT 1
                   FROM identity_proofing_api_keys k
                            JOIN identity_proofing_customers kc ON kc.id = k.customer_id
                   WHERE k.organization_id = c.organization_id
                     AND k.revoked_at IS NULL
                     AND kc.paused_at IS NULL);
    ```

    (`TestUpFoldsCustomersIntoOrg` runs it; it needs `customer_id`, which is
    gone after the migration);
  - one webhook endpoint, the oldest customer's that had one. A delivery still
    waiting for another customer is deleted, whatever URL it last named, since
    it would go to the kept URL under the kept secret; the kept customer's own
    stay, also those queued before it moved its URL. Delivered and failed ones
    stay as history (webhook health only counts what the current URL
    answered, §4). The kept Idempotency-Key answers are deleted;
  - one default flow, `is_default`, which an API call that names no `flowId`
    runs and the send forms preselect. A customer's default wins over the
    members' default, so an integration that names no flow keeps running the
    flow it ran: of the customers whose key still works (not revoked, the
    customer not paused) the oldest one's default, else the oldest customer's
    with a default. A data request flow cannot be the default, so a customer
    whose default was one is passed over. What this changes:
    - an org whose customers' working keys ran different defaults keeps one; a
      call with the other customers' keys that names no flow now runs the org's
      default (or answers 422 `no_default_flow`). Find those orgs before
      deploying with

      ```sql
      SELECT cf.organization_id
      FROM identity_proofing_customer_flows cf
               JOIN identity_proofing_customers c ON c.id = cf.customer_id
      WHERE cf.is_default
        AND c.paused_at IS NULL
        AND EXISTS (SELECT 1
                    FROM identity_proofing_api_keys k
                    WHERE k.customer_id = c.id
                      AND k.revoked_at IS NULL)
      GROUP BY cf.organization_id
      HAVING count(DISTINCT cf.flow_id) > 1;
      ```

    - a members' default replaced by a customer's no longer preselects in the
      form for a member: it preselects the org's default when members may use
      it, else the first flow they may use. A member's request always names its
      flow, so a member is never sent a flow the sender did not pick;

    the per-customer assignment (`identity_proofing_customer_flows`) is
    dropped;
  - every request: one that had a customer is `external`, one that had none
    `member`; already sent requests keep their stored purge time until their
    next write. Their children (a data request's matches and match checks,
    webhook deliveries, idempotency answers) and the API key a request names
    keep it by foreign keys on the org instead of the customer;
  - the identity match keys, which hashed the customer in, are cleared: the
    family name and birth date they were made of are not kept, so they cannot be
    made again. A session settled before the migration is therefore not found
    by a data request (§15);
  - what was paused stays paused, as a platform pause (§8): an org its own
    admin had paused, since they did, and an org whose every customer was
    paused, since the last of them was. A customer paused while others were not
    only loses its keys (above): a hosted link sent for it and not started yet
    can now be started, as the org's.

  Audit events recorded for customers stay in the log and still read
  (`customer_created`, `customer_updated`, `customer_flows_configured`,
  `customer_removed`). The down migrations rebuild one customer per org; keys
  stay revoked and pauses stay platform pauses.

## 2. The mail is the session

A session runs its flow's own lifetime when the flow sets one (2, 5 or 10
minutes), else `SessionTTL`, 10, the org's fixed default (`flowSessionTTL`),
within the
engine's hard cap `SessionMaxLifetime` (15), and its claim, the `handover`
token in the `vcmrtd://verify?handover=…&api=…` deep link, is single use and
claimable for `ClaimTokenTTL` (10 minutes, never past the session's own
expiry), so the mailed QR works for the whole session.

**Send.** `CreateRequest` creates the engine session (`TTL` = the flow's
`sessionTtlSeconds`, else the org's `session_ttl_seconds` for an external person,
else `proofing.SessionTTL` (600);
client reference = the request id), stores the request
with `link_expires_at` = the session's expiry, attaches the session
(`AttachSession`, audited `identity_proofing.session_created`; `flow_version`
becomes the version the engine pinned), and mails the create response's native claim:
the `qr` block and the button carry the **same** vcmrtd deep link, and the text
states `validMinutes`. A live Idem session with no native claim is an error and
nothing is stored (a Yivi session has none: it starts from the screen). Scan
the QR from inside vcmrtd; the button is for a mail read on the phone.
- **An engine session no request holds is erased** (`discardSession`), since it
  may carry the reference photo:
  - a send whose request fails to store or to attach it, or that got no vcmrtd link;
  - a hosted start that lost the race to attach (409 `link_started`).
  - It runs on its own context (`WithoutCancel`); a failed erase is logged and
    joined to the error returned.

The mail cannot re-mint the claim, and nothing restarts a session: when the
engine reports it `expired` or `cancelled` undecided, `EndSession` stamps
`ips_session_ended_at` (audited `identity_proofing.session_ended`) and the
request reads as expired. A new request means a new mail.

- **The cap follows the engine.** Opening a session gives it more time in the
  engine than it was created with (`SessionOpenTTL`, within
  `SessionMaxLifetime`). `SessionStatus` reports the engine's expiry
  (`Result.ExpiresAt`), and every reconcile moves `ips_session_expires_at`
  later to match (`ExtendSession`; never earlier). Without it a request read
  expired while its subject was still at it, then flipped to their outcome.
- **Cancel and purge end the engine session under the row's lock.**
  `RequestStore.Cancel` and `.Purge` take an `endSession` callback and call
  the engine inside their transaction, after their UPDATE took the row. The
  engine's own change notice for it then waits, and finds the request
  cancelled or purged instead of recording it expired: one cancel sends one
  final webhook. A failed engine call rolls the write back. A cancel runs on
  its own context once the engine has been asked. `MarkStarted` ignores a
  cancelled or ended request, so a late "opened" cannot follow either.

**Mail URLs.** A mail URL is otherwise absolute http(s) only. The proofing
link variable (`proofingUrl`) is declared with `AppScheme: "vcmrtd"`
(`internal/email/catalog.go`): its value must be a `vcmrtd:` link with a host or
an absolute http(s) URL, checked at render, and only that variable may carry the
scheme.

**App Link.** With `IDENTITY_PROOFING_APP_LINK_URL` set (e.g.
`https://idem.yivi.app/verify`), every claim link, in the mail, its QR and the
on-screen and hosted pages, is that https URL with the same `handover=` and
`api=` (`grantResponse` in `internal/proofingengine`). Mail clients and in-app
browsers keep it tappable, a phone camera opens it, and without the app it lands
on a fallback page. idem.yivi.app is served by open-yivi-ops; the Idem app
handles it from privacybydesign/vcmrtd#224 on. Empty keeps `vcmrtd://verify`.

The `api` host in the deep link is `IDENTITY_PROOFING_PUBLIC_URL`, which must
be reachable from the phone. A mail that fails to send leaves the request standing
but answers `mailSent: false`, and the UI says so.

Mail goes through the org's own SMTP settings (`internal/email`); in the dev
stack the seeded org points at Mailpit (`localhost:8025`), so nothing leaves the
machine until an admin sets a real server under e-mail settings.

## 3. Outcomes: notified by the engine, never polled

**Deployment:** the jobs here and the webhook deliverer wait on Postgres
`LISTEN`/`NOTIFY` (`database.RunOnNotify`, one connection held per job). That
needs a direct or session-mode connection: behind a transaction-mode pooler
(PgBouncer's default) a `LISTEN` is lost, and changes are then picked up only
at the next timed wake.

The engine calls `Service.SessionChanged` off its request path on each change
(opened, in_progress, verified, rejected, needs_review, cancelled, expired);
it reconciles that one request (`tryReconcile`, `SessionStatus`, §11). A session nobody finishes is reconciled at its cap by the deadline
job (`ReconcileDue`), which sleeps until the earliest cap and is woken by
`pg_notify` on `identity_proofing_sessions` when a session is attached; one the
engine still reports open past its cap is asked again after 30 s. The job leases
what it re-checks (`ips_reconcile_leased_until`, `FOR UPDATE SKIP LOCKED`), so
API replicas never re-check the same session at once. Nothing runs on a fixed
interval but the job's one-minute safety net (below). List reads never call the engine; a single-request read (the on-screen page,
the public API's `GET sessions/{id}`) re-checks a live request at most once
per `readReconcileEvery` (10 s), as a fallback for a missed notice. A change for a
session not attached yet is dropped; its deadline or next change reconciles it. `needs_review` is not final: a reviewer decides it, or the engine ends it (expired/cancelled), which ends the request as expired and sends
`session.expired`. The UI treats it as not live: it stops polling and shows
"waiting for review". The engine's `opened`/`in_progress` moves the request from `pending` to
`in_progress` (audited `session_started`); an attached session is reconciled
until it is seen to end or decide, also past its cap, so a last-moment outcome
is never lost. `expired` is
derived (no live session and no outcome), never stored. An engine failure during
a read is logged and the last known status is shown.

**Counts.** `GET /orgs/{slug}/identity-proofing/stats` counts the external
requests of the last `statsWindow` (30 days) per flow, by outcome, scoped like the
request list (an admin's the org's, a member's their own); members' own
requests are not counted. Expired is derived
in SQL exactly as `EffectiveStatus` does; cancelled requests have their own
count. The counts are as last reconciled: they read the rows, never the engine, so an outcome no list read has picked up yet is
not in them. The stats query key sits under the requests key, so whatever
refreshes the request lists refreshes the counts.

**No background polling.** `database.RunOnNotify` runs a job on a Postgres
NOTIFY or at the deadline the job returns, never on a ticker; the deadline job
and the webhook deliverer both use it. The deadline job sleeps at most
`deadlineSafetyNet` (a minute), so a request a replica leased and then died
with is picked up again without waiting for an unrelated notice, and it skips
(and logs) a request whose seal does not open instead of failing the rest.

**Shutdown.** The proofing jobs run on a context of their own that outlives
the HTTP drain (`proofingJobs` in `cmd/api`), so the notices and deliveries
the last requests cause still go out. A waiting `/events` poll answers at
once when shutdown starts (`Engine.StopWaiting`, registered with
`http.Server.RegisterOnShutdown`), instead of holding the drain past
`shutdownTimeout`. A configured Regula is pinged at boot, and a failed ping
fails the boot.

**Method and timeline.** The engine derives the app a subject used
(`methodOf` in `SessionStatus`; `proofingprovider` names the methods) from the
session's status, reading only each device's `role` and whether a
Yivi `disclosure` exists: `yivi_app` (a Yivi disclosure), `idem_app` (the
native device; vcmrtd is the Idem app), `browser` (the web device alone), or
none while no device claimed the session. It is stored on the request
(`method`) by `MarkStarted`, `RecordOutcome` and `EndSession`, and carried in
their audit snapshots. `GET /identity-proofing/requests/{id}/events` is the
request's timeline: its audit events (target `identity_proofing_request`),
oldest first, for an admin, or trimmed for the member who sent it
(`memberTimeline`): `IdentityProofingPersonalKeys` (subject, diplomas, a
reviewer's reason) leave the metadata, and any actor but the member becomes the
label `admin` ("An admin"), since the identity result is admin-only. Everything `reconcile`
records runs under `audit.WithoutActor`: the outcome is the subject's doing,
not that of whoever's read triggered the check. What the subject's app caused, `session_started` and
an outcome not decided in review, names that app instead (`subjectAppContext`,
actor label `app:<method>`, shown as "Idem app"/"Yivi app"); an expiry and a
post-review outcome stay the system's.

## 4. Public API, webhook, settings

- **API keys** (`identity_proofing_api_keys`, `apikey_store.go`) belong to the
  org: `GET` and `POST /orgs/{slug}/identity-proofing/api-keys`, and
  `DELETE .../api-keys/{keyID}`, admin only. `yp_live_` + 32 random bytes,
  stored as SHA-256 only, shown once; `prefix` tells keys apart. The page calls
  them API tokens. `createdByName` is the creator's display name, absent once
  that user is deleted. One key works
  on every flow and reaches every external request of the org. Revoke is
  permanent. Audited `api_key_created` (with its scopes) / `api_key_revoked` on
  the org (target `org_identity_proofing_settings`). There are no test keys
  (§5). No key is needed to send from the app.
- **Public API** (`/api/v1/proofing/{flows,sessions,sessions/{id}}`, Bearer
  key, no cookie): acts as an admin sending to an external person, on any flow
  a recipient can finish (`externalCompletable`; no `flowId` is the org's
  default flow, else 422 `no_default_flow`), refused while paused. It reaches
  the org's external requests only (`RequestStore.GetExternal`,
  `ListExternalPage`); a member's request is a 404 there. `sendMail:
  false` skips the mail; the create answer always carries the `deepLink`. A
  request made this way has `requested_by` NULL and `api_key_id` set, and every
  mail to an external person names the org as requester (`Settings.SignedAs`:
  the branding's display name, else the org's name), never the key or the member.
  `POST /proofing/sessions/{id}/cancel` ends a `pending`/`in_progress` session in
  the engine too (`cancelled_at`; status reads `cancelled`; audited `session_cancelled`).
  `DELETE /proofing/sessions/{id}` erases it in the engine (a session already
  gone is fine; one there under another token is an error, never erased) and clears its personal data
  (subject name and address, proofed name, the subject in its audit events;
  `purged_at`, audited `session_purged`, webhook `session.purged`); the row
  stays readable with `purgedAt` and its outcome. A CHECK
  (`identity_proofing_requests_purged_check`) refuses any write that puts
  personal data back on a purged row.
  `GET /proofing/sessions/{id}/result` (scope `results:read`) reads a settled
  session's identity and evidence from the engine on each call (`SessionIdentity`
  decodes the name, birth date, nationality, document type, issuing state and
  expiry, the checks, and the images: `photo`, `selfie`, `referencePhoto`,
  `documentImage` and `documentImageBack`; never the document number),
  audited `result_read`; 404 once erased. The public API never returns an image.
  An org admin reads the same shape in the wallet at
  `GET /orgs/{slug}/identity-proofing/requests/{id}/result`
  (`AdminRequestResult`, sharing `requestResult`), audited `result_read` with the
  admin as actor; a member's request is 404. For an approval, and for a check
  waiting for review (with its `identity` too, which the API shows only once
  approved), this response adds every image the engine still holds, `{mimeType, data}`, only PNG/JPEG/WebP
  (anything else, like an unconverted JPEG2000, is dropped): `photo` (the
  document's portrait, DG2 or the disclosed credential's), `selfie`,
  `referencePhoto` (the sender's own photo of the person, §14), and
  `documentImage` / `documentImageBack` (the document's front and back). A flow
  that does not keep face photos has dropped the face images after the match.
  The people table (`proofing-people.tsx`) reads it when
  an admin opens an approved, rejected or in-review row and folds
  it into the row's detail list with those photos: one audited read per open
  (`staleTime: Infinity`), and the timeline then shows it.
  Keys carry `scopes` (`sessions:write`, `sessions:read`, `results:read`,
  `flows:read`); every key gets all four, and only a signed-in org admin
  creates one. A missing scope is 403
  `insufficient_scope`.
  `GET /proofing/sessions?limit=&cursor=` pages the org's external sessions
  newest first from stored state (cursor: created_at + id), `{sessions, nextCursor}`.
  `Idempotency-Key` on create, cancel and the headless start (`idempotency.go`,
  `identity_proofing_idempotency_keys`, keyed by API key and Idempotency-Key, so
  two keys, of one org or of two, may use the same one and are never replayed
  each other's answer (`TestIdemKeyIsPerAPIKey`, `TestIdemKeyIsPerOrg`); pruned
  after 24 h, and dropped with the key):
  - the same key and body replay the first successful answer
    (`Idempotent-Replayed: true`); another body is 422 `idempotency_key_reused`;
    a call still running is 409 `idempotency_in_flight`.
  - a call that errs or panics releases the key, so it can be retried; a
    release that fails is logged, as the caller only sees the call's answer.
  - without an encryption key a keyed call is 409 `no_encryption_key`, like
    an unkeyed one.
  - a key is never run again once the call may have had its effect: an answer
    that fails to store, or a replica lost mid-call, keeps the key in flight
    (409) until the 24 h prune. There is no timeout release.
  - the stored answer is sealed and linked to its session (`request_id`, read
    from the answer's `id`); `RequestStore.Purge` deletes it.

  Headless: `POST /proofing/sessions/{id}/methods/{idem_app|yivi_app}` starts a
  `hosted: true` session in that app from the org's own UI (`appLink` or
  `walletLink`; not hosted is 409 `not_hosted`); `GET …/methods/{m}/status`
  answers from stored state. The hosted token is the poll token: the Yivi
  disclosure poll and a new Idem code run through `/proof/{token}/…`.
  Sessions are named by an opaque `ps_` id (base32 of the request UUID,
  `public_id.go`) on the public API and in webhooks; the org routes take
  either form.
  What a key causes is audited with the key as actor: `audit_events.actor_label`
  `api_key:<prefix>`, no `actor_user_id`; the audit log shows "API key <prefix>…".
  Rate-limited per org (`internal/ratelimit`, in-process token buckets,
  so per API replica): `apiCallLimit` 120 calls a minute, and of those
  `APISessionLimit` 10 session creations, so one org cannot use up the
  deployment's capacity. Past it: 429 `rate_limited` with `Retry-After`. The
  hosted page has its own per-org bucket (§7).
  - The key lookup is a read; the org's limit is taken before the key's
    `last_used_at` is written (`Touch`), at most once a minute
    (`apiKeyTouchEvery`).
  - An unknown key counts against its source (`authFailureLimit`, 60 a
    minute): past it the guess answers 429 instead of 401. It never holds a
    valid key, so a shared proxy address is never locked out by someone
    else's guesses. A source is the client address `clientip` resolves behind
    `TRUSTED_PROXIES`, an IPv6 one its /64 (`SourceKey`); the first request
    with `X-Forwarded-For` from a peer the list leaves out is logged once, as
    every caller would then share the proxy's limit.
- **Webhook**: one endpoint per org, for the events of all its flows
  (`identity_proofing_webhooks`, one row per org, secret sealed with the
  proofing key, `whsec_…`, shown once). Admin only: `GET`, `PUT` and `DELETE
  /orgs/{slug}/identity-proofing/webhook`, `POST .../webhook/rotate-secret`,
  `POST .../webhook/test` and `GET .../webhook/deliveries` (the 20 most recent).
  Removing the endpoint deletes the deliveries still waiting for it. The org's
  results show in Sessions, the API and the audit log whatever its webhook.
  Only external requests send webhook events: a member's request sends none. An
  org without an endpoint is sent no events (nothing is queued; `test` needs an
  endpoint).
  - Rotating the secret keeps the previous one signing next to the new one for
    24 h (`webhookSecretOverlap`, `previous_secret_*`): the signature then
    carries a `v1` for each, so the receiver can switch without refusing a
    delivery.
  - A delivery's outcome is recorded only while its lease holds (same
    attempts and `next_attempt_at` as claimed): a worker whose lease lapsed
    records nothing over the newer outcome.
  - Without `IDENTITY_PROOFING_ENCRYPTION_KEY` the deliverer does not run, and
    the API says so once at boot. Events
  `session.created` / `.started` / `.handover` / `.verified` / `.failed` /
  `.review_opened` / `.expired` / `.cancelled` / `.purged` / `.diploma_added`
  (`WebhookEvents`) are written to the
  outbox (`identity_proofing_webhook_deliveries`, keyed by org) in the same transaction as the
  change (`AttachSession`, `MarkStarted`, `RecordHandover`, `RecordOutcome`,
  `EndSession`, `LapseLinks`, `Cancel`, `Purge`, `DiplomaStore.Add`); an
  endpoint saved before an event existed is not subscribed to it. `test` is sent on
  request whatever is subscribed. The payload is the session id, status, flow,
  method, assurance and error code: never a name or address (the API has those).
  A delivery's `sessionId` is the `ps_` id (absent for a `test` delivery). Each outbox insert
  `pg_notify`s `identity_proofing_webhooks`, so the deliverer sends as the change
  commits; it then sleeps until the next retry or lapsed lease. It leases due rows (`FOR UPDATE SKIP LOCKED`, 5-minute lease).
  - Per batch (`deliveryBatch` 20) `claimDue` takes at most `endpointWorkers`
    (2) of one endpoint's deliveries, its oldest (a `row_number()` per
    org over everything due, held rows counted, so across replicas too).
    `deliveryWorkers` (10) send at once, `sendFair` keeping to 2 per URL. The
    next batch waits for the last, so an endpoint that never answers costs
    every other org one `safehttp.RequestTimeout` per batch, not its backlog.
  - Worst batch: one round per endpoint plus 2 rounds of all workers,
    3 x `safehttp.RequestTimeout` = 30 s, inside the lease
    (`TestDeliveryBatchFitsLease`).
  - A delivery whose endpoint secret no longer decrypts counts as a failed
    attempt, so it fails in time and drops its diploma, instead of being
    claimed every 5 minutes forever.
  - `POST …/webhook/test` is 10 a minute per org (`webhookTestLimit`).

  It POSTs
  through `safehttp` (https, public addresses only, dialed IP = vetted IP, no
  redirects) with `Yivi-Signature: t=<unix>,v1=<hex HMAC-SHA256("<t>.<body>")>`
  (a second `v1` during a rotation's overlap),
  `Yivi-Event`, `Yivi-Delivery`; 8 attempts over about 24 h, then `failed`.
  Health is one `WebhookHealth` for the org (`not_configured`, `delivering`, or
  `failing` since, with the last status code and the pending retries), derived
  from the deliveries last sent to the endpoint's current URL (a delivery's
  `endpoint_url`, which `claimDue` sets): what an earlier URL, or a customer's
  endpoint the migration dropped, answered does not count
  (`TestWebhookHealthCountsCurrentURL`); a failing endpoint shows as an alert on
  the Overview (for an admin) and on the Developers page. The failing streak counts
  from a delivery's `first_attempt_at` (or the last success, if later):
  `last_attempt_at` moves with every retry.
- **Settings** (`identity_proofing_settings`, one row per org): `GET` and
  `PATCH /orgs/{slug}/identity-proofing/settings` (any of `erasureHandling`,
  `legalRetention` (null removes it), `allowedRedirectOrigins`; absent is
  left as is; `sessionTtlSeconds` or `dataRetentionDays` is 400
  `invalid_input`, `checkSettingsChange`), `PUT .../settings/branding` (multipart) and `GET .../settings/logo`.
  Any member reads them (the verify page needs the branding); an admin writes
  them. Audited `identity_proofing.settings_updated` on target
  `org_identity_proofing_settings`, with before and after (whether a logo is
  set, never its bytes; a replaced logo adds `logoReplaced`); saving what it
  already holds changes and audits nothing. Held in `OrgSettingsStore`; no row
  reads as the defaults. A session change sets only what it names, applied under
  the advisory lock on the org (`lockSettings`), and a `PATCH` checks every part
  before it saves any, then saves the session settings, the legal retention and
  the redirect origins in one transaction with one audit event (`UpdateSettings`,
  `OrgSettingsStore.Save`, `TestSettingsSaveIsOneTransaction`). The white-label
  columns and `erasure_handling` were columns of the customers: migration
  `20261021100000` merges them into the org's settings (§1).
  - **Session**: `session_ttl_seconds` (600) and `data_retention_days` (7:
    "purged 7 days after a check ends", VC3) are fixed defaults
    (`SessionTTL`, `defaultDataRetentionDays`), which a flow overrides in the
    flow editor, and `erasure_handling` (`review`/`automatic`, default
    `review`, §15). Migration `20261021110200` set the retention column's
    default to 7, as `defaultDataRetentionDays` is, and first gave an org that
    had sent an external request without saving settings a row with the 30
    days it had then. `keep` (refuse every erasure) is gone: the legal
    retention says what the law keeps; migration `20261021110100` made it
    `review`. The lifetime and the retention were the org's to set until
    migrations `20261021114000` to `20261021114200`: those gave every active
    flow version that set neither a new version carrying the org's value where
    it was not the default (an org without a settings row was already on it),
    stamped its unpurged external requests without a retention of their own
    with it (`retention_override_seconds`), then set every org to 600 seconds
    and 7 days. No flow or sent request changes; a member's request on such a
    flow now follows the flow's copied retention rather than
    `proofedNameRetention`. The Downs leave the copies, since the org's own
    values are not given back. The editor's lifetime options are held to the Go
    list by `lib/identity-proofing.test.ts`. In the app "Who decides", the legal
    retention and the redirect origins are on Settings › Data and privacy.
  - **Legal retention** (`legal_retention_months` 1 to 600, with
    `legal_retention_reason_en` and `_nl`, all or none; Settings › Data and
    privacy, `routes/settings-privacy.tsx`): how long the law makes the org keep
    the data of every approved identity check, counted from the end of the
    check, with the reason shown to the person in each language. It holds only
    an approved external identity check (`legalHeld`): one that was rejected,
    expired, cancelled or never started proved nobody, so it is purged at its
    own retention and blocks no erasure. It is the floor of such a check's
    retention (§10): the check is purged at the later of its flow's or the
    org's retention and the legal period, and the engine session, the proofed
    name (once approved, `RecordOutcome`) and the days a person is told follow
    (`engineRetention`, `subjectRetentionDays`, `LegalRetention.longest`). A
    data request, a member's request and another org's are not held. The
    period counts calendar months as the database does (`addMonths`); the form
    states years or months, and switching it off or shortening it asks to
    confirm first (`legalRetentionLoss`). `Service.UpdateSettings` checks it
    with the rest before saving any (`LegalRetention.check`), and saving it
    moves the purge time of the org's unpurged external requests under the
    settings lock (`SaveLegalRetention`). Its Down migration puts `purge_at`
    back to the retention alone.
    - **Engine retention.** The engine fixes a session's retention when it
      creates it, so a save sets it again for the org's unpurged identity
      checks (`syncEngineRetention`, `Provider.SetSessionsRetention`). The save
      marks the org (`engine_retention_stale`) in its transaction; the sync
      runs under the same settings write lock, reads the settings under it,
      and clears the mark once every batch succeeded
      (`OrgSettingsStore.SyncEngineRetention`). Two saves at once therefore
      leave the engine at what was saved last, and a failed sync is not the
      update's failure (logged): the hourly `PurgeDue` syncs every org still
      marked (`syncStaleEngineRetention`). A check created against settings
      that changed before its session was attached marks its org too
      (`markIfRetentionMoved`), since a sync that ran in between did not see
      it.
    - **The org's own purge** (`PurgeRequest`, `DELETE
      /proofing/sessions/{id}`) refuses a check the legal retention still
      holds: 409 `legal_retention` (`RequestStore.HeldUntil`).
  - **Branding**: display name, `#rrggbb` colour, logo (PNG/JPEG/GIF/WebP,
    512 KiB; no SVG, mail clients do not render it), support contact, https
    privacy URL, and whether to hide "powered by" (§7). It applies to the
    proofing mail of external people (a member's mail is the org's own):
    subject, heading and requester are the display name, the colour replaces
    the org's theme primary seed, the logo replaces the org's theme logo (none
    shows the name as wordmark, never the theme logo), and the `supportContact`
    / `privacyUrl` paragraphs appear only when set (an all-empty paragraph
    collapses). The hosted page and the on-screen page show the same display
    name, colour, logo, support contact and privacy URL.
  - **White-label** (the same branding save): a second colour
    (`secondary_color`, the Idem app's), the palette the org theme has (text,
    surface, border, link, success, warning, error and top bar colours) and a
    font family, all `#rrggbb` or a CSS font-family list; NULL keeps the
    design's own. Any colour or the font is a look of its own
    (`whiteLabeled`); a logo, name, support contact and privacy link alone are
    not. It applies to the Idem app and the hosted page (below).
  - **Idem app branding** (`proofing/app_branding.go`, engine `branding.go`):
    the app session view carries `branding`, so the app looks like the
    requester's own: colours (accent as secondary, top bar as header), font and
    logo, and `GET /app/{token}/branding/logo` serves the logo to a device
    holding the session. It is resolved from the session alone (tenant = org,
    client reference = request), never from the request, through
    `AppBrandingResolver` over `RequestStore.SubjectKindOf` (the subject kind
    and the request's flow), the org's settings and the flow's own branding: a
    request to an external person gets the proofing branding its mail and
    hosted page wear, the flow's own over the org's (`Settings.brandedFor`).
    With white-label (a colour or the font, a flow's own colour included) that
    is its colours, font and logo (the flow's, else the org's proofing logo);
    what it leaves out keeps the design's own, not the org's theme's. Without,
    the org theme under the name the proofing mail signs with, with the
    proofing logo (the flow's, else the org's) or else the theme's. A request
    to a member gets the org theme under the org's name, whatever its flow.
    Nothing set at either level is no `branding`. `relyingParty` stays the
    org's own name next to it. The hosted page applies the same look (`theme`
    in the hosted view).
  - **A flow's own branding** (`identity_proofing_flow_branding`, one row per
    org and flow, `flow_branding.go`; a row is "custom for this flow", none is
    "same as the organisation"): `GET` and `PUT
    /orgs/{slug}/identity-proofing/flows/{flowID}/branding` (multipart; `custom`,
    true or false, is required) and `GET .../branding/logo`, admin only. It holds
    the org's name, colour, logo, support contact and privacy URL; the
    white-label colours and font and "powered by" stay the org's. Each field it
    sets replaces the org's, and one left empty, or no logo, falls back to it;
    the org's white-label colours and font stay under it (`Branding.over`,
    mirrored by `effectiveBranding` in `lib/identity-proofing.ts`). It styles
    the flow's proofing mail (`proofingMail`: a branding that cannot be read
    blocks the mail, a logo that cannot be read leaves the wordmark), its
    hosted page (`HostedRequest`, `HostedLogo`; where `SetAppBranding` is
    wired, as in `cmd/api` and the integration harness, the page's `theme` and
    logo come from `AppBrandingResolver`, which applies it as well) and the
    Idem app of an external person. The on-screen page, a member's mail and a
    member's app session still show the org's. Going back to "same as the
    organisation" deletes the row, logo included, after the tab asks to
    confirm, and checks none of the form's fields or its logo upload (the
    handler does not read it): the tab sends its hidden draft text fields in
    that mode too. Audited `identity_proofing.flow_branding_configured` with
    before and after. The form is the Branding tab of Edit flow, with a preview
    that switches between the proofing mail and the Idem app (`appLook` in
    `lib/identity-proofing.ts` mirrors the resolver for an external person).
  - **Allowed redirect origins** (§7).
  - Branding is under Settings › Branding (the org's, for every flow; the
    wallet's own look is the separate Settings › Wallet theme).

## 5. No test mode

There are no test keys, scripted outcomes or `livemode`: every key and session
is real. `IDENTITY_PROOFING_PROVIDER=stub` is the only stand-in, for dev and
tests (§0).

## 6. A new Idem code mid-session

`POST …/requests/{id}/claim-link` and `POST /proof/{token}/claim-link` call the
engine's `SessionHandover`. An unclaimed slot gets a new claim (the first
lapsed after 10 minutes); a slot whose app went inactive or silent gets a
handover, and the phone that scans takes the session over. Only a handover (the
engine answers `slotClaimed`) is audited `identity_proofing.session_handover` (the
member, or `hosted_link`) and sends `session.handover`; a fresh claim is not. An
app still active is 409 `device_active`. A Yivi request has none
(`wrong_method`).

The app's own `POST /app/{token}/handover` mints at most 20 a minute per
session (`HandoverLimit`; past it 429), as each one is audited. A device
mints for its own slot at will, but for the other slot only while that slot
is free or its device is away (409 `device_active` otherwise), as above.

The on-screen page follows the phone by itself: `GET …/requests/{id}/app` reads
the engine's status live (`devices[].current`/`away`) as `waiting`, `connected` or
`away`, polled every 2 s. It shows the claim QR while `waiting` (renewed once
`deepLinkExpiresAt` or the last code lapses), hides it while `connected`, and
mints a handover QR the moment the app is `away`; the app coming back drops that
code (the engine cancels the grant). If `app` cannot be read it falls back to the
page's own code. That code, like the hosted page's, lasts the whole session, so
neither page offers a new one while it shows; a hosted page opened with no code
in the window (a reload) still has "Show a new code".

## 7. Hosted link

`POST /proofing/sessions` with `hosted: true` (public API)
creates no engine session: it stores the request with a link token (only its
SHA-256, `link_token_hash`) valid `hostedLinkTTL` (72 h) and answers
`hostedUrl` = `APP_BASE_URL/p/<token>`, which the org hands its subject.
Nothing is mailed. The public page `/p/:token` (`routes/proof.tsx`) runs the
same steps as the on-screen page (`routes/proofing-verify-steps.tsx`, shared):
what is collected, the app, then its session. Its API is `/api/v1/proof/{token}`
(`hosted_handler.go`), unauthenticated like `/vog/{token}` and rate-limited as
the API is:

- The link is looked up once per call (the limiter hands the request it read
  to the handler). A live link's call counts against its org's
  `hostedCallLimit` across all its links; a link no longer live
  (`Request.linkLive`: not waiting to start, its session not running or in
  review, no diploma or export window open) counts against its own
  `staleLinkLimit` (60 a minute), so an old link polled hard leaves the org's
  live pages alone. Every call also counts against its source's
  `hostedSourceLimit`; an IPv6 source is its /64 (`clientip.SourceKey`).
  Past either: 429 `rate_limited` with `Retry-After`.
- Its own bucket, not the API's, sized for the page (status and Yivi polls
  every 2 s).
- As with an unknown API key, a token that matches no link is a 404 that counts
  against its source's `authFailureLimit`; past it, 429.

Its routes: the page, `status`, `start` (once: a second start is 409
`link_started`), the org's `logo`, and the Yivi `start`/`disclosure`
routes, which share their implementation with the org routes. The session is
created at `start` for the app the subject picked (`AttachSession` records it)
and runs its flow's session lifetime, else the org's fixed 10 minutes. An
unstarted link reads as pending until
it lapses, then expired, in `EffectiveStatus` and the stats alike.

A member makes the same link from the wallet: "Copy a link" in the Verify a
person dialog (`channel: hosted` on `POST .../requests`, which answers
`hostedUrl`; for an external person only), for a
person not present and a flow the mail cannot carry (diplomas); the dialog
shows the link to copy (`SecretReveal`) once it has closed, on one line, with
"Open in new tab" beside Done. Opening it only loads the page (`GET`
`/proof/{token}` reads); the link is used by `start`, when an app is picked.
No redirect: the page ends on its own done screen.

A link that lapses unstarted is ended by the deadline job (`LapseLinks`,
audited `session_ended` with `reason: link_lapsed`, webhook `session.expired`).

**Consent and completion.** The overview step is the consent screen (who asks,
what is collected, why, retention, privacy link). It never shows the flow's name
or its assurance level, and the hosted view leaves both out of its `flow`: the
token alone opens it. The retention it states is the
server's `retentionDays` (hosted view `flow`, flow list):
`subjectRetentionDays`, the flow's override else the org's days, plus the
engine's day of grace, rounded up ("deleted at most N days after"); a flow that
collects nothing (only the outcome) states none. **Continue only moves on to
the app step, on every flow** (#304): opening the link or pressing Continue
starts nothing. The app step lists the apps `methodChoices` gives (Yivi app and
Idem app when `yiviAvailable`, else the Idem app alone, still as a step of its
own; `yiviAppAvailable`, so an age check at an age the Yivi credentials carry
no claim for is Idem-only), and tapping one is the start (no radio, no confirm button; Back is
disabled while it runs). The on-screen page follows the same steps. The step
headings are h1 on the hosted page (`StepRegion headingLevel`) and h2 under the
on-screen page's title. The overview's Decline asks to confirm first (`ConfirmDialog`:
"Go back" or Decline, saying the link stops working; it stays open while the
request is in flight), then calls
`POST /proof/{token}/decline`, which cancels a link not yet started
(`DeclineHosted` → `RequestStore.Cancel`, audited `session_cancelled` with the
actor label `hosted_link`); a started link is 409 `link_started`. The org
lists `allowedRedirectOrigins` (org settings, `PATCH .../settings`; stored
`identity_proofing_settings.allowed_redirect_origins`): https origins, or http
on a loopback host, at most 10, normalised lowercase. The host must be a DNS
name or an IP address (`validHost`): the origins are written into the page's
`frame-ancestors`, where `*`, a quote or a `;` would mean something else. A hosted create may carry
`redirectUrl` on one of them (else 400 `redirect_not_allowed`; any other channel
400). The page view answers `sessionId` (the `ps_` id), `redirectUrl`,
`embedOrigins`, `language` and `organization` (name, branding and data
retention; the branding is the flow's own over the org's). Once `Session` reports an outcome
(`onSettled`), `proof.tsx` posts `{type: "proofing.completed", session, status}`
to its parent for each allowed origin (never `*`), then redirects
(`location.replace`, so back does not land on the link and redirect again) with
`?session=&status=` when there is a redirect; otherwise it shows its own
outcome (`cancelled` included). A session that ran out, or a link that lapsed
unstarted, shows "This check has ended" instead (`HostedCheckEnded` in
`proof.tsx`, VF5) and stays on it: an `expired` check is posted to an embedder
but never redirected (`hostedHandBack`). It says nothing was shared, for a
started session how long it ran, and who to ask for a new link, with the
support contact from the branding (the flow's, else the org's; a mail or web
address is a link, a web one in a new tab because the page can be framed). How
long it ran is the lifetime the session was created with, not today's
settings: the engine reports it (`proofingprovider.Session.TTL`, the asked TTL
within its bounds), `AttachSession` keeps it (`ips_session_ttl_seconds`), and
the hosted progress carries it as `sessionTtlSeconds` once started; a session
started before it was kept says "not finished in time" without a number. The
on-screen page's ended state is the same `CheckEnded` with Start again. The
hosted card sits at the top, 80 px down, on every step. A data request in
review is not handed back: the page says it is in review. The page's language
is the request's
`language` (also passed to the engine at start), then the browser's, then English
(`lib/hosted-completion.ts`). It switches i18n without persisting the wallet's
stored choice. Framing: the proofing handler is a `server.PageHeaderer`, so the
SPA handler asks it for headers on each index fallback; on `/p/<token>` it sets
`Content-Security-Policy: frame-ancestors <allowed origins>`, or `'none'` for no
origins, an unknown or throttled link, or a failed read (counted against the
link's org's `hostedCallLimit`). Only where the API serves the SPA (`STATIC_DIR`); in
dev Vite serves it without the header. The rest of the SPA still sends no CSP.
The page's outcomes, failures and errors are live regions (`role="alert"`
for a failure, `status` otherwise); the diploma step keeps one mounted for
each upload's verdicts, and `StepRegion` moves focus to the new step's
heading. The deep-link gap of §11 applies: the page
shows the QR, and says so.

**Per-flow hosted settings** (the `hosted_*` columns of
`identity_proofing_flow_settings`, `flow_hosted.go`; flows belong to the
provider, so keyed on org and flow id with no foreign key): `enabled` (off: a
hosted create is 409 `hosted_disabled`), `locales` (empty: every one; a create
with another `language` is 400; the page keeps to them), and `completion`
(`redirect`, or `done`, where a create with a `redirectUrl` is 400).
Admins edit them on the Hosted page tab of a flow's Edit flow view, with a
preview of the overview step in the flow's branding; Cancel there discards
that tab's changes only, so Edit flow and the other tabs' drafts stay. The
languages show ticked when none is saved, and all ticked saves as none. No row
is the defaults.
**Theme:** the page applies `theme` through `applyOrgTheme` (`hostedTheme`)
while it shows, with the branding's own `primaryColor` (the flow's, else the
org's) over the theme's, and a "Powered by Yivi" line the
org can leave off (`hide_powered_by`, Settings › Branding).

## 8. Pausing an org's proofing

Only a platform admin pauses an org's proofing (`identity_proofing_org_pauses`,
`pause.go`): `PUT /admin/organizations/{id}/identity-proofing/pause`, the
"Identity proofing" column on `/admin/organizations`, list at
`GET /admin/identity-proofing/pauses`. The org cannot pause or resume itself,
and nothing below the org (a flow, a person) can be paused. Any member reads
`GET /orgs/{slug}/identity-proofing/pause`, and the Overview shows why proofing
is stopped (`proofing-pause.tsx`). Default: never paused. Audited
`identity_proofing.paused` / `.resumed` with `by: platform`. While paused,
nothing new starts: `CreateRequest` (a member's send, the API's create) and
`startHosted` (a hosted link's start, the API's headless start) answer 403
`proofing_paused`. Every other route stays open, so an admin who paused over a
leaked key can still revoke it, remove a webhook or erase data. Running sessions
still settle (engine notices, the deadline job, an on-screen Yivi disclosure)
and webhooks still go out. Pausing rejects every open review of the org
with `ORG_PAUSED` (reviewer
"system: organisation paused"), so nothing waits on a decision nobody can
make; the pause's confirmation says so. No review stays open under a pause:

| When | What |
|---|---|
| Before the pause is set | Every open review is rejected; one that fails refuses the pause. |
| Right after it is set | Open reviews are listed and rejected again (one reached in between). |
| A session reaches review while paused | `tryReconcile` rejects it at once (`rejectIfPaused`). The pause is read after the review is written, and the pause lists reviews after it is set, so one of the two sees the other. |
| A rejection there failed | The hourly `PurgeDue` rejects the reviews still open in every paused org (`rejectPausedReviews`). |

The pause records who set it (`PlatformPausedBy`). The
columns of an org's own pause (`org_paused_at`, `org_paused_by`) are dropped.
Migration `20261021101000` turned a pause an org had set itself, and the pause
of every customer of an org, into a platform pause that names nobody (the
notice then reads "the platform administrator"); the hourly sweep rejects
the reviews such an org still had open. Audit events recorded with
`by: organization` stay in the log. `Stores.Pauses` nil
(unit tests) never pauses.

## 9. Manual review

A session in review (`needs_review`) waits for a decision (or for the engine to
end it, §3). An admin, or a member with the reviewer right (below), decides it on
Identity proofing › Reviews (`routes/proofing-reviews.tsx`; the sidebar and the
overview count what waits).
The page lists what waits, oldest first (waiting since = `completed_at`, when it
went to review), under All, Identity checks and Data requests; Closed lists the
data requests that ended, whoever ended them. The list carries no mark of a
reviewer's decision (the audit event is not read), and a data request is also
rejected with no reviewer: the engine rejects the person (`FACE_NO_MATCH`, ...),
the review lapses (`REVIEW_LAPSED`), the org pauses (`ORG_PAUSED`) or must keep the
data (`LEGAL_RETENTION`). So the tab and a closed request's page say it ended, not
that a reviewer decided, and the Outcome column shows the error code in words.
An identity check has no closed list yet, as none goes to review today. A data
request shows "Answer by" = `purge_at`, a calendar month after it went to review
(`completed_at`, which "Asked on" shows while it waits), when it lapses (§15).
The list is `GET .../requests?status=needs_review`: the filter is applied before
the list's cap of 200, and that list is the longest in review first, so the cap
leaves out the newest, never the most overdue. A count from a capped list shows
"200+" (`listedCount`, `requestListCapped`), on the tabs and the sidebar. Closed reads
`?flowKind=data_request&status=approved` and `...&status=rejected`, each narrowed
before its cap. A row opens its review page, `reviews/{requestId}`: for an identity check
(`identity-check-review.tsx`) why it waits, the checks the engine ran, the data
from the chip (an audited result read) and the decision; for a data request §15.
The decision is approve or reject with a required reason; a rejection names what
is wrong, sent as the error code (`REJECT_REASONS` in `lib/proofing-reviews.ts`):
`REVIEW_NOT_HOLDER` (it is someone else's), `REVIEW_ALTERED` (it looks
altered), `EVIDENCE_NOT_AS_REQUESTED`, none for "Something else", which is
`MANUAL_REVIEW_REJECTED`. These are the reviewer's own codes
(`reviewerRejectCodes`, any other is 400 for a reviewer): a wallet code would
mean something else to the person, and `IDENTITY_MISMATCH` would hide the
session from a later data request (`Candidates`). A reviewer does not decide an
identity check they sent or that is about them (403 `own_review`; the page
shows a note in place of the decision); a data request's link sender may
decide it, or a one-admin org could never answer it.
`POST .../requests/{id}/review` calls the engine's
`DecideReview` (the reviewer's e-mail as `reviewer`; the engine keeps who
decided, why and when with the session, `session.ReviewDecision`), audits
`identity_proofing.review_decided` with the reviewer as actor, then reconciles:
the outcome lands like any other, so an approval still has to meet the flow's
assurance level and the org's webhook is sent (for an external request). A request no longer under
review is 409 `not_under_review`. A decision leaves the page, so it marks the
result read stale without reading it again (`refetchType: "none"`): each read is
audited, and the next page that opens the request reads the outcome. Two
reviewers deciding at once are serialised (`LockReview`). An identity check goes
to review only for something no signature or authoritative register vouches for:
an unsigned upload, a value typed in by hand. Today nothing built produces that,
so only data requests (§15) and the dev stub reach review, and the identity
check page has no "What to check" box for the unsigned item: it needs the item
from the wallet, which none yet carries.

**The reviewer right.** An admin gives a member the right to decide in the
member's edit page, "Can review" (`PUT .../members/{userId}/review-right`,
`memberships.can_review`, audited `membership.review_changed` only when it
changes, and subscribable in Notifications like `membership.role_changed`). The
right is a member's: a PUT on an admin is 409 `admin_reviews`, a role change
clears it (audited too), so an admin made a member again has to be given it
anew, and `memberships_can_review_member` holds every row to that.
`organization.MayReview` is an admin whose mandate is not withdrawn, or a member
with the right, and `organization.RequireReviewer` gates the four `reviewer(...)`
routes in `proofing/handler.go`: `review`, `result`, `data-matches` and
`data-export`. Everything else stays a member's. A reviewer who is not an admin
reaches org-wide only what the Reviews page shows (`ReviewScopeReviews`): a
request waiting for review, and a data request (`data_access`, `data_erasure`)
in any status. An identity check that is not waiting holds a person's identity
and photos the right was never given for. So `GET .../requests` lists every
request in the org only for `?status=needs_review` (Waiting) and for
`?flowKind=data_request` (Closed reads it with `status=approved` and `rejected`);
any other list is the requests they sent. So are the stats, All people and the
member routes. `GET .../requests/{id}` opens one they sent or one in that scope
(`mayOpen`, judged on the stored request through `Service.RequestIf`, before the
engine is asked), and `result`, `data-matches`, `data-export` and the decision
take the same scope (`reviewScope`); any other is a 404, as for a member. The
app reads `canReview` from `GET /orgs/{slug}` (the same `MayReview`): it shows
the sidebar's Reviews item and count, the Reviews and review pages, and the
Overview's **Waiting for your decision**, which lists what waits in the org as
the sidebar counts it. The Reviews footer links to Members for an admin only, as only an admin
reads that page. Not yet: the review link on a row of All people and the flow's
People tab is an admin's.

**The Overview and the dashboard are to-do lists** (`identity-proofing-overview.tsx`,
`dashboard.tsx` and `dashboard-todo.tsx`, built from `ui/task-list.tsx`).
The Overview lists, up to five rows each: what waits for the reader's decision
(an admin and a reviewer; a link to Reviews); the external checks nobody has
finished (`pending`, `in_progress`), the longest waiting first; and the latest
that ended (`approved`, `rejected`, `expired`; a withdrawn one is no news). A member
reads the same lists about the checks they sent, as `GET .../requests` already
scopes them. A member who cannot decide sees their own checks held for review under
**Waiting for the person** as "Waiting for an admin", first, so the five rows never
cut them off. The lists wait for the org's `canReview` before they load. The dashboard's **To do** holds
a row for what waits for the reader's decision and, for an admin, for the members
who have not identified and those whose VOG needs attention
(`lib/member-insights.ts`); **Waiting on others** holds the unfinished checks and,
for a member who cannot decide, the ones held for an admin. A row appears only
with a count above zero. The line pointing to Settings › Organisation is an admin's,
as only an admin opens that page.

Each list is its own read, `GET .../requests?subjectKind=external&status=<s>`, one
per status (`useExternalRequestsInQuery`, `todoStatuses`): `?status=` matches the
status a request reads in (`EffectiveStatus`, in SQL `effectiveStatusColumn`), so a
link that lapsed unstarted is `expired`, not `pending`, and cannot use up the 200 cap
of the running ones. A count from a read that hit the cap shows as at least
that many, "200+" (`listedCount` and `requestListCapped` in
`lib/identity-proofing.ts`, `cappedAmong`). While a platform admin has paused the org's
proofing, only **Verify a person** is hidden on both pages (`todoShows`): every list
route stays open, and the Overview puts why above the lists. The pure parts
(`lib/proofing-todo.ts`) are tested; the counts come from the same lists as the
Overview.

## 10. Data minimisation

Stored and audited: status, the engine's assurance tier, achieved eIDAS level,
error code (the reason, shown in words by `proofingRejectionReason`, whose
table `identity-proofing.test.ts` holds to the engine's `errCode…` and the
wallet's `error…` constants both ways; name a new code as a constant there). Each
outcome is its own action, `identity_proofing.approved` / `.rejected` /
`.needs_review`, so a rejection never reads as a success. Every request event
names its subject
(`subjectName` when sent with one, `subjectEmail`). Never the other document fields, BSN or images.

What the wallet reads from the engine is not what it keeps. Reconciling reads
`SessionStatus`, which carries no personal data. It reads the identity
(`SessionIdentity`) to compare an expected subject (§13) and for a decided or
in-review external session's match key (`recordIdentityKey`: an HMAC of the
family name and birth date, §15), the full result (`SessionResult`) for the
name it keeps (below), and the identity with its images on each result read
(§4). Of those reads it keeps only the sealed name and that HMAC.

The name is the one exception, and only for an **external person** once the
session is **approved**, or for a data request once its person is proven and
it goes to review (the sender may know only an address): it is sealed
under `IDENTITY_PROOFING_ENCRYPTION_KEY` in `proofed_name_ciphertext`, shown as
`proofedName`, never audited. A member's request, a rejected one, or an
identity check in review keeps no name.

Under a flow's BlurBSN policy (or a mask or omit BSN policy) a document photo
is released and stored only with the BSN region the app located filled solid;
a photo that came without one is withheld from the result and dropped from
storage (`coverBSN`), since the server never guesses where the BSN is. Only a
Dutch document prints a BSN, so a photo of a document known not to be Dutch
is released and stored as taken; one whose issuing state is not known is
treated as Dutch (`coversDocumentBSN`).

A session is purged by the `identity_proofing_purge` pruner
(`Service.PurgeDue`) its flow's retention override (kept on the request,
`retention_override_seconds`) or, without one, for an external person's request the org's
`data_retention_days` (§4; `defaultDataRetentionDays`, 7, without a settings
row), or for a member's `proofedNameRetention`
(30 days), after it ends (completed, cancelled, expired, or gone to review: an
undecided identity review is kept no longer than a decided one; a data request
in review lapses a calendar month after it went there, `dataRequestReviewMonths`,
instead): as `DELETE`
above, erased in the engine, then its personal data cleared here (the subject's
name and address, the proofed name, the redirect URL and Yivi transaction, its
diplomas and stored idempotent answers, a data request's own matches), the
reviewer's reason in its audit events included. The expected birth date
(§13) goes as soon as the session ends, is cancelled or its link lapses. The
dashboard shows the time as `purgeAt`.
- **`purge_at` is stored** (a column of `identity_proofing_requests`), so the hourly
  `ListPurgeDue` reads a partial index (`purged_at IS NULL`) instead of
  computing the time for every unpurged row.
  - The store computes it, not a trigger: `setPurgeAt` in
    `request_store.go` is the one rule (retention after settling, a data
    request's review lapse after `dataRequestReviewMonths`, an approved erasure
    when it was decided; the legal retention is a floor of an approved external
    identity check's retention, the later of two moments, not of two intervals:
    PostgreSQL compares a month to 30 days, `TestLegalCountsCalendarMonths`).
  - Every write to one of its inputs calls `refreshPurgeAt` on the same
    transaction, after the write:

    | Write | Input it changes |
    |---|---|
    | `Create` | the row (link lapse, flow retention) |
    | `AttachSession`, `MarkStarted` | session cap, status |
    | `EndSession`, `LapseLinks`, `Purge` | `ips_session_ended_at` |
    | `Cancel` | `cancelled_at` |
    | `RecordOutcome` | status, `completed_at` |

    A new write to one of those columns must call it too;
    `TestPurgeAtSetOnEveryWrite` checks each against the rule.
  - An org's changed `data_retention_days` (`OrgSettingsStore.SaveSession`;
    no longer reachable from the API, the value is fixed) recomputes the purge
    time of its unpurged external requests that follow its retention, with no
    flow override (`refreshOrgPurgeAt`). Lock order (rule at `lockSettings`, an advisory lock
    on the org: an org that saved nothing has no settings row to lock):

    | Transaction | Settings lock | Request rows |
    |---|---|---|
    | `SaveSession`, `SaveLegalRetention` | exclusive (`settingsLockWrite`), first | then all unpurged external ones, `FOR NO KEY UPDATE` in id order |
    | `RequestStore.Create` (external person) | shared (`settingsLockCreate`), before the insert | its new row |
    | Request writers (`Cancel`, `RecordOutcome`, `Purge`, ...) | none | their row |
    | `ClearExpiredProofedNames` | none | due rows in id order, `FOR NO KEY UPDATE` |

    - The exclusive and the shared lock conflict, so no request is created
      against the old retention and left stale (`TestRetentionWaitsForInsert`);
      inserts share it and never wait on each other.
    - No other writer takes the lock, so none waits on it while holding a
      request's row: a request writer holding a row the retention change waits
      on still enqueues its webhook and commits (`TestRetentionBesideWrite`).
    - Statements that wait on several request rows lock them in id order;
      `LapseLinks` and `ListDue` use `SKIP LOCKED` and never wait.
  - A running session's cap is stored before it passes; the purge asks
    `purge_at <= now()`, which holds only after it anyway. `purgeAt` in the API
    stays empty while the session runs.
- **A purge of a stale read is refused** (`errPurgeSessionMoved`): a request
  read before a hosted start attached its session. `Service.purge` reads it
  again and erases that session first.

## 11. Known gaps and open items

Everything this feature does not do yet, in one place.

- **Faces.** No browser face step: the engine has a web slot, but the wallet
  has no browser proofing page for it, so only flows whose face capture runs in
  the app (`selfieLocation: native`) can be sent; the wallet forces `native` and
  refuses others (`flow_not_completable`). No face engine of its own: a flow's
  face step needs Regula.
- **Deep link.** A tapped vcmrtd deep link is not confirmed to open the app
  (on either platform; a fix there is the vcmrtd app's):
  - It is the mail's button and "Open in the Idem app" on the on-screen and
    hosted pages.
  - Scanning the QR from inside the app works, so each page shows the QR and
    says to scan it from inside the app.
- **Only Dutch driving licences** (§ Whose identity): no other state's
  licence CSCAs are embedded, and there is no EU licence masterlist to load.
- **Residence permits** are their own document kind, never an identity card:
  - The chip access key names `residence_permit`; Passive Authentication runs
    the ICAO path (`mrtdEvidence.documentType: icao`) against the same
    master lists. The NL list holds `CSCA NL, OU=Ministry of Security and
    Justice` (the IND's ministry).
  - `flow.DocumentKindOf` derives the kind from DG1/MRZ, as vcmrtd's
    `td1DocumentType` does: same nationality and issuing state is an identity
    card; a permit code (`IR`, `AR`, ...) is a permit.
  - A foreigner's card with a plain ID code (`I<`, `ID`) has no kind: BE, DK
    and PL put `ID` on permits, Estonia on resident EU citizens' identity
    cards. The app keeps the user's pick there; the server does not take it.
  - On the Yivi path the credential (`pbdf.residencepermit`) gives the kind.
  - `AcceptedDocumentTypes` takes codes or kinds, case-insensitively; the
    result's evidence carries `documentKind`.
  - Not yet read from a real permit chip.
- **Hosted page:** no full-theme endpoint; no automated accessibility test
  (axe, WCAG 2.2 AA), which needs a rendered DOM the frontend tests do not have.
- **Manual review:** when an identity session goes to review is a product
  decision; today only data requests do (§9).
- **Diplomas (§12):** no mailed hosted link for a diploma flow (the mail is the
  Idem session); no diplomas on a member's request page; no real DUO extract in
  the test suite (personal data), so the parser is covered by synthetic layouts.
- **One known person (§13):** the hosted and on-screen pages do not name the
  expected person to the subject.
- **Reference photo (§14):** not sent from the wallet's own send form; not
  combined with the expected-subject match, which needs the document data.
- **Data requests (§15):** no matching across orgs (each org is the controller
  of its own sessions); no mail to the person when decided; no data requests on
  members' own proofing requests; only an admin (or an API key) sends one. An
  erasure cannot take a `probable`, `email` or `name` match: those wait for the
  org's retention. A deployment without Regula cannot create the standard flows
  (they check the face), so it cannot send a data request.
- **Chip Authentication** over DG14 is not run: `nfc.chip_auth` is checked
  through Active Authentication only (`chip_verify.go`), so a chip with
  Chip Authentication and no AA key is not proven genuine against cloning.
- **Yivi app:**
  - It reaches low only, so it is offered on no flow requiring substantial;
    a Yivi path to substantial is planned.
  - Its disclosure (`proofingQuery`, `dcql.go`) asks no document code,
    issuing country or expiry, so a flow that restricts the country, or the
    document codes, refuses every Yivi disclosure; one listing kinds, as the
    editor saves them, is held to the credential's kind.
- **Yivi route end to end.** It does not work end to end yet. Still to do:
  - require the device token on the Yivi routes, as on the Idem ones;
  - give a failed hosted Yivi session a restart (R26);
  - make the face check after the disclosure optional per flow.
- **Handover (R14, decided 2026-10-05).** A handover is minted from the screen
  (`SessionHandover`, once the device holding the slot is inactive or stale) or
  by that device itself (`POST /api/v1/app/{token}/handover`); redeeming one
  moves the slot and revokes the old device. Restricting who may mint one is a
  later feature. The hosted page does not follow the app (`waiting` / `connected` /
  `away`) as the on-screen page does, so its session QR stays shown.
- **Custom domains** for the hosted page are not built: the page is on the
  wallet's own domain with the org's branding and white-label (§4).
- **Settings still open:**
  - which fields the sender must send with a session, and what a mismatch
    does, cannot be set per flow in the editor (§13 is per session);
  - raising the org's retention after a session moves the wallet's purge,
    not the engine copy's, and the subject was told the old number.
- **Rate limits** are a fixed bucket per org (`apiCallLimit`,
  `APISessionLimit`, `hostedCallLimit`), per replica. Planned: `RateLimit-*`
  headers, and API usage per org ranked by key.
- **App long-poll.** `GET /app/{token}/events` re-reads the session every
  `SessionEventsPoll` while a client waits; a change notice would spare those
  reads.
- **Tests without a DOM:** `SecretReveal`'s `dismissible={false}` has no
  render test.
- **Planned features:** who sees what (member, admin, platform admin); audit log filter and export (the buttons exist); DigiD for the
  address, compared with what the person gave (Idem route); anti-glare for document capture; `high` assurance, only with a
  certified liveness vendor.
- **Before launch:**
  - The DPA and the controller and processor chain with each org.
  - The Regula licence for production.
  - Verifying the EU Trusted Lists' XAdES signatures: §12 trusts them on
    transport security alone (`diploma/eutl`).
  - Confirming the Idem app only uses the step endpoints (the single-shot
    `/result` is refused).

## 12. Diplomas (DUO extracts)

For onboarding where a qualification matters (training for professionals: a
new student proves who they are, then which diplomas they hold). Wallet-side
only: the engine knows nothing of it.

- **Per flow** (`identity_proofing_flow_settings.diplomas`, `FlowDiplomaStore`,
  `PUT .../flows/{id}/diplomas`, admin): `off` (the default) or `required`,
  audited `flow_diplomas_configured`. A step is in the flow or not, so there is
  no optional diploma step. `required` needs a flow that reads the name and
  date of birth (`flow_no_identity`): each extract's holder is matched
  against them. `identityRequired` holds such a flow to it in `EditFlow` (the
  spec must name `dg1`) and `ActivateFlowVersion` too (a data request flow is
  never edited, §15); the editor locks Name and document details on
  and saves a setting that lifts the rule before a version that drops `dg1`.
  The flow editor shows it as the last step, "Upload diplomas (DUO)": a step
  for the admin, never sent to the engine (its steps are the app's, and it
  would refuse one it does not know). It is saved after the flow, so a new flow
  gets it once it has an id; the flow list's summary appends it to the
  engine's steps. The flow list carries it as
  `diplomaMode`; a single version's answer (create, edit, versions) omits it.
  A request snapshots it at send (`identity_proofing_requests.diplomas`), as
  it does the assurance level.
- **A page, never a mail.** The extracts are uploaded on the page that ran the
  session, so a diploma flow is refused on the mail channel and as a bare
  deep link (`diplomas_need_page`, 422): on screen or hosted only. The send
  form switches to on-screen by itself (`sendableByMail`).
- **Upload** (`POST .../requests/{id}/diplomas`, `POST /proof/{token}/diplomas`,
  multipart `file` parts): only on an approved request, within
  `DiplomaUploadWindow` (1 h) of its approval (`diplomasUntil` in the
  request, hosted progress), at most 10 per request, 5 MiB each; the hosted
  route reads a body only as large as the extracts the request still takes
  (`HostedDiplomasOpen`, `maxDiplomaUpload`). The page
  shows the step after an approval and hands back (`onSettled`, the hosted
  redirect) only once the subject is done: at least one extract held, or the
  window closed.
- **The check** (`internal/diploma`, as in `privacybydesign/go-diploma-issuer`,
  which has the detail): DUO's PAdES signature is verified first, on the raw
  bytes: it must cover the whole file, carry a qualified timestamp (the
  timestamp's own signer must be trusted) and chain on the EU Trusted Lists to
  DUO's qualified e-seal. An unsigned file is `not_a_diploma`, and only a file
  that passed reaches PDFium, on its own pool apart from VOG's
  (`newDiplomaPDFiumPool` in `cmd/api`, at most 2 instances), killed past 20 s. The printed holder (one line, `MatchFullName`)
  and birth date must match the identity the engine approved, read with
  `SessionIdentity` at upload (the chip's DG11 name when it has one, as the MRZ
  cuts a long name off); each name word matches plain or ICAO 9303
  transliterated, so "Müller" matches MUELLER and MULLER, and the given names
  must be the same ones (one may list only the first of the other's), not
  merely share the first.
  - The timestamp counts only when its signer chains to a qualified timestamp
    service (TSA/QTST) on the lists; the ESS signing-certificate-v2 attribute
    must name the signer by its hash.
  - **Revocation fails closed**: every certificate below the anchor is checked,
    online by default (OCSP, else the CRL; a stale or expired answer is
    refused), or with `DIPLOMA_OCSP=false` against the revocation information
    embedded in the extract (within 7 days of the signing time). A certificate
    neither can check fails the extract.
  - `DIPLOMA_VALIDATOR_PROVIDER=duo` is the default; `stub` (the dev stack,
    `DEV_MODE` only, which holds no DUO-signed extract of a test person)
    accepts every signature but still parses and matches for real.
  - `DIPLOMA_TRUST_SOURCE` is `eutl` (default) or `pinned`. The lists are
    fetched over https only (redirects included), cached in
    `DIPLOMA_TRUST_CACHE_DIR` (a cache up to 7 days old stands in for a failed
    fetch), and reloaded in the background once 24 h old; a failed reload keeps
    the anchors already loaded and is retried after 15 minutes. The pinned roots
    are only the boot fallback.
- **Kept**: what DUO printed about the qualification (type, name, profiles,
  institution, place and date, NLQF/EQF, the number duo.nl/diplomacontrole
  checks, signing time) in `identity_proofing_request_diplomas`. Never the
  PDF, the holder's name or birth date. Audited `diploma_added` (webhook
  `session.diploma_added`) and `diploma_rejected` (reason only:
  `not_a_diploma`, `signature_invalid`, `holder_mismatch`, `duplicate`).
  Shown in the request responses, the public API's `result`, and the
  people table. `Purge` deletes them with the rest.


## 13. One known person (expected subject)

For a check that must be one particular person, not whoever takes part: an
account recovery (the org's "wachtwoord vergeten" sends its user to a hosted
session) or "Dibran Mulder must identify himself". Wallet-side only: the engine
proofs whoever scans, and the wallet holds the outcome to the person.

- **Send.** An external person's request with a `birthDate` (YYYY-MM-DD, past, with
  `name`): the Verify a person dialog's "Only let this person pass" switch (shown only for a
  flow that reads the document data), which asks for the name and the date of birth,
  `POST .../requests`, and the public API's
  `POST /proofing/sessions`, on every channel. A request to a member cannot
  (`ErrInvalidInput`: the wallet holds no birth date of a member), nor a flow
  whose result lacks the document data (`readsIdentity`, the same name in the
  backend and the frontend: `dg1` requested). Without a birth date the name stays a
  label.
- **Kept.** `expects_subject` on the request, and the birth date sealed under
  `IDENTITY_PROOFING_ENCRYPTION_KEY` in `expected_birth_date_ciphertext`
  until the request is decided (`RecordOutcome` approved/rejected), its
  session ends, is cancelled or its link lapses, or it is purged;
  `needs_review` keeps it for the decision. Audited only as
  `expectsSubject: true` on `requested`, never the date.
- **The match** (`matchSubject`, in `tryReconcile` after `enforceAssurance`):
  on an approval it reads `SessionIdentity` and requires the same full name,
  word for word (`diploma.SameName`, plain or ICAO-transliterated; no missing
  or extra name), and the same birth date. The chip's DG11 name counts as
  well as the MRZ's (`Identity.FullGivenName`/`FullFamilyName`, kept through
  the finish in `ParsedMRZ.FullSurname`/`FullGivenNames`): the MRZ cuts a long
  name off at its field's width (30 characters on an ID card). A chip without
  DG11 is matched on the MRZ name alone. A mismatch is recorded as
  `rejected` with `IDENTITY_MISMATCH` (no proofed name kept); a failed
  identity read leaves the request undecided for the next reconcile. A
  review approval is matched too. The engine still holds its approval, so
  the result read (`requestResult` → `heldToVerdict`, public API and admin
  view alike) answers the wallet's rejection and code with no identity or
  images: the other person is never shown (`ASSURANCE_NOT_MET` likewise).
- **Shown.** `expectedSubject` on the request, the public API's session
  and its webhooks; "Expected person" in the people table's details; the
  reason in words (`proofingRejectionReason`).

## 14. The org's own photo (reference photo)

For an org that already holds a photo of the person and wants a live face
check against it without the document (an insurer confirming a policy
holder's change of bank account).

- **Flow.** A face step without `nfc_read` (`NeedsReferencePhoto`; the
  editor's "Face check" alone). `Completable` stays false for it (a member
  has no photo to send); `externalCompletable` is true, so an admin or an API
  key can send it to an external person, and the flow lists carry
  `needsReferencePhoto`.
  It runs in the Idem app only (`yiviAppAvailable` false: the Yivi app
  matches against its credential's photo) and reaches no eIDAS level (the
  levels need the chip; the engine scores it with the sender's photo as the face's
  source, `matchedRelyingPartyPhoto`).
  The send form leaves it out.
- **Send.** Only the public API: `referencePhoto`, standard base64,
  sniffed as PNG/JPEG/WebP and decoded when the engine session is made
  (`checkReferencePhoto`; one that does not decode is 400, not a failed
  face step later): at send, or for a hosted request at its subject's start,
  since the wallet does not decode images itself; at most
  `maxReferencePhotoBytes` (512 KiB;
  the create body is capped at 1 MiB, an idempotent one past that is 413
  `body_too_large`). Required on such a flow (422
  `reference_photo_required`), refused on any other (400), so a chip flow is
  never matched against a supplied photo. Audited as `referencePhoto:
  true`, never the image.
- **Held.** The engine's session keeps it sealed (`session.ReferencePhoto`)
  and matches the face step against it at Regula (`faceMatchReference`),
  never handing it to the app; `Session.ForStorage` drops it once the session is
  under review or over (the result's copy, below, is all that stays). A hosted request
  holds it sealed (`reference_photo_ciphertext`) only until its subject
  starts (`AttachSession`), cancels, lets the link lapse, or it is purged;
  `RequestStore.ReferencePhoto` reads it at start only, never with a list.
- **In the result.** The session's result marks `faceReference:
  relying_party` (the evidence reads `reference_photo`, not `emrtd`) and,
  when the flow requests the selfie, releases the photo beside it
  (`referencePhoto`), kept and purged with the selfie: the people table
  shows "Photo sent with the request" next to the selfie. The public API's result
  carries no image, as ever.

## 15. Data requests (GDPR access and erasure)

A person asks the org what is held of them ("see my data", Art. 15) or for
its erasure ("delete my data", Art. 17). See my data and Delete my data are a
standard feature of every org, not flows anyone sets up: the org sends the
person a session on its standard flow of that kind, like any other session
(API, hosted link, mail or on-screen); the person proves who they are, and the
session goes to the org's **needs review**, where an org admin or a reviewer
decides (§9). Code:
`standard_flow.go`, `data_request*.go`; frontend `routes/data-request-review.tsx`
(its review page), the Verify a person dialog's "What do you send?" and the
hosted page.

- **Standard flows.** One flow per org of each kind, `data_access` and
  `data_erasure` (`identity_proofing_flow_settings.kind`, `standard` true; a
  partial unique index allows one per kind, a CHECK none of kind `identity`).
  The wallet creates them (`Service.standardFlows`) the first time an org
  needs them: `GET .../data-request-flows` (admin; the dialog and the on-screen
  page read them there) or a send of a data request. That is lazy rather than
  a backfill because the flows live in the proofing engine, which validates
  and stores a flow only through its own interface (a migration would have to
  write the engine's definition JSON), and it covers new and existing orgs
  alike. Their definition is the wallet's (`standardFlowSpec`): document scan,
  chip read and a face check (Regula, liveness) at eIDAS Substantial, releasing
  the document data (`dg1`, the name and date of birth a request finds the
  person's sessions by), `chip_checks` and `biometrics`, never an image and
  never the age check, which would withhold that date of birth, each with the
  wallet's reason in every hosted language (`standardReasons`, §1's reasons per
  item); legal basis `legal_obligation`. A standard flow whose active version
  falls short of it (`meetsStandard`, which needs `readsIdentity`: `dg1`
  without `age_over`; a version saved before reasons existed included) gets it
  back in a new version under its own name, and one the engine no longer lists
  is replaced. Both are audited
  `flow_created`/`flow_version_created` and `flow_kind_configured` without an
  actor. Two first sends at once each create a flow; `ClaimStandardFlow`
  (an advisory lock per org and kind) makes the first the standard one and
  leaves the other a hidden data request flow.
- **Not a flow anyone edits.** No flow becomes a data request any other way
  (the flow editor's Type and `PUT …/flows/{id}/kind` are gone). A data
  request flow is not in `GET .../flows`, the API's `GET /proofing/flows` or
  any flow picker, and `EditFlow`, `ActivateFlowVersion` and the hosted,
  branding, diploma and address saves refuse it (409 `data_request_flow`). It
  is never a members' flow (`data_flow_for_member`, a CHECK and both service
  paths) nor the org's default flow (a CHECK, and 400 `invalid_input` from
  `validSelection`); it asks for no diplomas and no address.
- **Sending one.** `flowKind` `data_access` or `data_erasure` on
  `POST .../requests` (the dialog) and the API's `POST /proofing/sessions`
  runs on the standard flow of that kind, created first if need be
  (`requestFlowID`); only an admin or an API key sends one
  (`flow_not_allowed`), only to an external person. A `flowId` that names a data
  request flow (an API caller that kept a standard flow's id) runs on the
  standard flow of its kind, and a `flowKind` that is not the named flow's kind
  is 400 `invalid_input`. A request keeps the kind it was sent with
  (`identity_proofing_requests.flow_kind`).
- **Flows from before (migration 20261021122000).** Only the seed made data
  request flows (it no longer does). An org that has flows of a data request
  kind keeps the oldest of each kind as its standard flow: the one whose first
  engine version is oldest, else (a flow the engine does not hold, as under the
  in-memory stub) the one whose settings were written first. The others stay
  data request flows: hidden, not editable, and a request naming one runs on the
  standard flow of its kind; their past requests keep pointing at them. Every
  data request flow's hosted page goes back to the default (on, every language,
  ending on the redirect), since nobody can open its Hosted page tab any more;
  Down drops the mark and leaves the hosted pages on.
- **The subject's page.** A data request refused after the person proved who
  they are (`dataRequestRefusal`: `MANUAL_REVIEW_REJECTED`, `REVIEW_LAPSED`,
  `ORG_PAUSED`, `LEGAL_RETENTION`) reads "Request refused" with the reason in
  the subject's words, not "Not verified". The outcome lines read the live
  progress `Session` hands `onSettled`, never the page as it loaded. Errors on
  the hosted page go through `hostedErrorMessage`: what the subject cannot act
  on reads "This link can't be used right now".
- **To review, not approved.** When the engine approves a data request's
  person, `tryReconcile` calls `findDataMatches` and records `needs_review`
  (sending `session.review_opened`) instead of approving; it keeps the proofed
  name like an approval. From then on the wallet owns the decision: a later
  engine read (approved, or the session purged at the engine) changes nothing.
- **Matches.** `findDataMatches` reads the person's identity and every
  `Candidates` session of the **org**, external ones only (not purged, settled; the
  person's earlier data requests too once decided, as each holds who asked, so
  an erasure clears them; one still in review never) with the person's
  identity match key. A match needs the same full name word for
  word (`diploma.SameName`) and birth date: `strong` when document type,
  issuing state and expiry also agree (the same document; the document number
  is never read), `probable` otherwise. The org's unfinished external sessions
  (pending, in progress, expired, cancelled; `UnfinishedCandidates`) hold no
  proofed identity, only what the sender typed, and are listed apart:
  - `email`: sent to exactly the same e-mail address, case aside.
  - `name`: otherwise, the typed name is the person's full name word for word
    (`diploma.SameName`).
  Stored in `identity_proofing_request_matches`, whose composite foreign keys
  keep both requests within the matching request's org. One reconcile at a
  time finds a request's matches (a hosted status poll meanwhile waits), and
  what it read per candidate is kept (`identity_proofing_request_match_checks`)
  so a retry reads none again.
- **Identity match key.** `identity_match_key` is an HMAC of the org, the
  normalised family name and the date of birth, under a secret derived from
  the encryption key. It is set when an external person's session is decided
  or goes to review (`recordIdentityKey`; a data request's by its own search)
  and cleared by the purge. The migration that removed the customers cleared
  the keys made per customer, so older sessions have none and are not
  candidates.
  - `Candidates` selects on it, so only sessions with the person's family
    name and date of birth are read from the engine.
  - It links one person's sessions at one org for a database reader, who can
    already link them by the plaintext `subject_email`; it never links across
    orgs.
- **Review.** The review page shows the person's identity and proof (name,
  date of birth, document, chip, face match, liveness, eIDAS level; the
  result read allows a data request in review, `heldToVerdict`) and the
  matches. A match whose name and date of birth were read off a verified chip
  is a sure one (`sureMatch`: `strong`, or `probable` on another document);
  `email` and `name` ones are not. An erasure starts with its sure matches
  ticked, an access request with its `strong` ones; any other is taken only
  when ticked (`approvedMatches`, `matchTickedByDefault` in the frontend), and
  an erasure may take any match a reviewer ticks. `DecideReview` → `decideDataRequest` (decided in the
  engine too if it holds a review there). Approving an erasure asks to confirm
  the number of sessions, then purges each approved match through
  `Service.purge` (row stays, personal data goes, `session.purged` per
  session), then the request itself. Access sets `data_export_until`
  (`DataExportWindow`, 7 days; 24 h through the hosted link). Reject records
  `MANUAL_REVIEW_REJECTED` unless a code is given.
- **How an erasure is decided** is the org's `erasure_handling`
  (`ErasureHandling`, in `identity_proofing_settings`; "Who decides" on the
  Data requests card of Settings › Data and privacy; `handleErasure`, run once
  as the request reaches review, under the review lock):
  - `review` (default): a reviewer decides, as above.
  - `automatic`: the sure matches are erased at once. Without unsure ones the
    request is approved and purged then (reviewer `automatic`); with them it
    waits for a reviewer, who decides only those (the erased ones show
    "Erased" and stay ticked).
  What the law makes an org keep (Art. 17(3)(b)) is its legal retention, not a
  handling: `keep`, which refused every erasure, is gone. A failure there
  leaves the request in review. An approved erasure stores how
  many matches it kept (`data_matches_kept`), and the hosted page says so
  instead of "Your data was deleted."; a refused one says the org must
  keep the data by law. Its `purge_at` is its decision time, so a final purge
  that failed is retried by the hourly `PurgeDue`.
- **Legal retention** (§4). An erasure takes no session the org's legal
  retention still holds (`DataMatch.HeldUntil`, from `legalHeldUntil` in
  `data_request_store.go`: the end of the check plus the legal period).
  `approvedMatches` leaves a held session out of the default choice and refuses
  a tick on one (`ErrLegalRetention`, 409 `legal_retention`); the review shows it
  unticked with its date. When every identity check still found is held (and
  one is), `declineHeldErasure` declines the request as it reaches review,
  without a reviewer: rejected `LEGAL_RETENTION`, decided by `automatic`. The
  person's earlier data requests among the matches do not count: each holds
  only who asked and goes at its own retention, so it sends nobody to review.
  If some identity check is free the request goes to review as before; an
  `automatic` erasure likewise leaves a held session alone and erases the free
  sure ones. Either decision records what the law still holds
  (`data_matches_held`, `data_held_until`, `DataHold`), which outlives the
  request's purge. The person's page then shows how many sessions are kept,
  the org's reason in their language and the date the last of them goes
  (`legalRetention` in the hosted progress, `withLegalNotice`, GDPR Art.
  12(4)), on a declined request and on an approved one alike, until that date.
  "See my data" is never held.
- **Nobody reviews it.** A data request left in review a calendar month (GDPR
  Art. 12(3); `dataRequestReviewMonths`, counted in UTC: 31 Jan lapses at the end
  of February) is rejected `REVIEW_LAPSED` and purged by `PurgeDue`, at the
  "Answer by" the Reviews page shows. An identity
  review counts its retention from going to review, and lapses the same way
  then: it is kept no longer than a decided one.
- **Export.** An approved access request's data: per approved session the
  outcome and its `match`. Only a `strong` match also carries identity and
  evidence, the address, and diplomas: a ticked `probable`, `email` or `name` match may be
  someone else's session. The contact the sender gave (the name and address
  it typed) goes out only for a strong match that was also approved for the
  one person it was sent to (`DataExportSession.Addressed`). Images are named in
  `imagesHeld`, never included. For the person on their
  hosted link (`GET /proof/{token}/data-export`, `dataExportUntil` in the
  progress; 24 h), for the admin (`…/requests/{id}/data-export`) and for the
  org's software (`GET /proofing/sessions/{id}/data-export`). Audited
  `identity_proofing.data_exported`.

## 16. Postal address

For onboarding that needs where the person lives. Wallet-side only, like the
diplomas (§12): the engine knows nothing of it. The address is what the subject
declares, never read off a document; a Dutch one is checked against the BAG.
A Yivi app request also asks the registered (BRP) address.

- **Per flow** (`identity_proofing_flow_settings.address`, `FlowAddressStore`,
  `PUT .../flows/{id}/address`, admin): `off` (the default) or `required`,
  audited `flow_address_configured`.
  - The flow editor shows it as "Enter the address", before the diploma step;
    saved after the flow and before the diplomas' follow-up save.
  - The flow list carries it as `addressMode`; a single version's answer omits
    it. A data request flow asks for none.
  - A request snapshots it at send (`identity_proofing_requests.address`).
- **A page, never a mail.** Like the diplomas: refused on the mail channel and
  as a bare deep link (`address_needs_page`, 422); `sendableByMail` checks both.
- **Entry** (`POST .../requests/{id}/address`, `POST /proof/{token}/address`,
  JSON, 204):
  - Once, on an approved request, within `AddressWindow` (1 h) of the approval
    (`addressUntil` in the request and the hosted progress, gone once entered;
    the hosted progress says `addressGiven`, never the address).
  - Street, house number, postal code, city and country, each required, trimmed,
    at most 200 characters.
  - Postal code and house number come first in the form. Once they are a Dutch
    postal code and a number, the page looks them up
    (`GET .../address/lookup?postalCode=&houseNumber=`, both routes, only while
    the address is open): every BAG address at that number, letters and
    additions included.
    - The typed house number is one of them: street, city, postal code and
      country fill in, once per postal code and number typed.
    - It is none of them: the page lists them to pick, or says none exists.
    - Not Dutch: nothing is asked, the fields are filled in by hand.
- **Checked** on entry (`verifyAddress`, `internal/addresslookup`), the result
  in the address as `verification`:
  - A Dutch postal code (`1234 AB`, which no other country writes) is looked up
    by postal code and house number; the house number (letter and addition
    included) and street must match, case and punctuation aside.
    - Found: kept as the BAG writes it, `verified`, country `Nederland`
      whatever was typed (the form fills it in the page's language).
    - Not found: 422 `address_not_found`, nothing kept. The BAG has an address
      from its building permit on, so a real one is never missing.
    - Register unreachable: kept as entered, `unchecked`, logged.
  - A country naming the Netherlands (`dutchCountries`: `NL`, `NLD`,
    `Nederland`, `Netherlands`, `Holland`, any case) with any other postal
    code: 422 `address_not_found`, so a mistyped one is not kept `unsupported`.
  - Any other address: kept as entered, `unsupported`.
  - An address kept before this has no `verification`; the page shows it as
    declared.
- **Registered address** (BRP), Yivi app only: the Idem app has no way to the
  BRP yet.
  - A request that asks the address starts its disclosure as
    `ScopeProofingAddress`: the identity plus `pbdf.gemeente.address` (street,
    houseNumber, zipcode, city) as an optional credential set
    (`required: false`), so a holder without it still proves who they are.
    - Issued by Gemeente Nijmegen after a DigiD login, for anyone in the
      Netherlands, as SD-JWT VC since 2026-06-01: a card loaded before then
      needs reloading.
    - Only in the production `pbdf` scheme, so its vct never follows
      `EUDI_CREDENTIAL_SCHEME`. Staging asks `pbdf-staging` identity plus the
      production address in one presentation.
    - The verifier must list `pbdf.gemeente.address` in
      `verifier_attestation_vcts` (`openid4vc-poc-ops`).
  - `yiviDisclosure` seals it in `registered_address_ciphertext` before the
    engine decides, so the page never enters an address before it is there.
    `AddAddress` and `Purge` clear it.
  - Nothing reads it yet: the entered address keeps its BAG result.
- **Config:** `ADDRESS_LOOKUP_URL` (default PDOK's public API, keyless), pinged
  at boot. A lookup the register cannot answer is 503
  `address_lookup_unavailable`; the page asks for street and city by hand.
- The address window keeps a hosted link live (`linkLive`), so the lookups
  run on the live link's rate limit, not the stale one's.
  - The page shows it after the approval and before the diplomas; it hands back
    (`onSettled`) once the address is kept or the window closed.
- **Kept** sealed in `address_ciphertext` (the proofing cipher), decrypted on
  every request read. Audited `address_added` without the address; the webhook
  `session.address_added` carries it.
  - Shown in the request responses, the public API's `result`, a data
    access export (strong match only), and the Sessions page.
  - `Purge` clears it and strips `address` from the request's webhook
    deliveries.
