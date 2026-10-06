# Identity proofing: what is still open

Branch `docs/identity-proofing-design` (PR #267). The wallet runs identity proofing itself, in
`backend/internal/proofingengine`, driven by `backend/internal/proofing`; how it works is in
`.ai/features/identity-proofing.md`. Everything the PR reviews of 2026-10-02 and 2026-10-06 found has been
fixed or decided, except what is open below. The full review checklist, the decisions with their answers and the old design
sections are in this file's git history (`git log -p -- .ai/plans/identity-proofing.md`).

PR state on 2026-10-06:
- Review threads of 2026-10-02: all resolved.
- Dobby's review of 2026-10-06 (26 threads at `05ca40d`): its 15 bugs, 2 nits and 8 of its 9 style
  findings are fixed. The ninth, moving the held-credential status re-check and history to its own PR,
  is decided below (Process): the PR is not split.
- Ruben's "Questions for the author" are settled in the code.
- The `Yivi Design System.zip` deletion is intended: the archive is not needed.

## Waiting on the user

- [ ] **When does the engine send a real session to review?** Today only data requests go to
  `needs_review`; an identity session never does. A trigger needs a decision (which check, which score).

## Later (decided to wait)

- [ ] **EU driving licences (decision 4).** There is no CSCA source for licences, so every genuine
  licence ends `DOC_TAMPERED`, and its Passive Authentication runs without the country cross-check
  (`mrtdverify.VerifyPassive`). go-passport-issuer has a working driving-licence path to copy. Until
  then a licence is refused at `document_capture` and `nfc_read` (422 `document_unsupported`), and the
  seeded flows no longer name it.
- [ ] **The Yivi app route (decision 6).** It does not work end to end yet. Then: require the device
  token on the Yivi routes, as on the Idem ones; give a failed hosted Yivi session a restart (R26); make
  the face check after the disclosure optional per flow.
  - The proofing disclosure (`openid4vpverifier.proofingQuery`) asks no document type, issuing country
    or expiry, so a flow that restricts the type or country refuses every Yivi disclosure
    (`document_not_accepted`, `disclosureComplianceFailure`). Ask for those claims once the passport
    and ID card credentials are known to carry them; a disclosed expiry is already checked.
- [ ] **Liveness retries.** How many attempts a session gets is the Idem app's concern; the engine caps a
  face step at `maxFaceStepAttempts` (3) as a safety bound.
- [ ] **Who may mint a handover (R14).** Decided 2026-10-05: the handover stays as it works now.
  - A handover is offered from the screen only (`SessionHandover`): the on-screen request page mints
    the QR once the app is `away`, the hosted page through "Show a new code".
  - It is never started from inside the Idem app. `POST /app/{token}/handover` stays, for the app's
    unchanged contract, but no phone shows a handover code.
  - Restricting who may mint one, or an in-app handover, is a later feature.
- [ ] **Custom customer domains** for the hosted page.
- [ ] **Hosted page accessibility test** (WCAG 2.2 AA with axe). Needs a browser DOM, which the frontend
  tests run without by design (`vitest.config.ts`).
- [ ] **BSN default.** An unset BSN policy keeps the BSN (`retrieve`); `omit` as the default is open.
- [ ] **Matching in the flow editor.** Which fields a customer must send with a session, and what a
  mismatch does, set per flow. Today a customer can send one known person (name and birth date, held to
  the outcome as `IDENTITY_MISMATCH`), but the flow cannot require or configure it.
- [ ] **Chip Authentication (DG14) is never verified.** A chip with DG14 and no DG15 (German passports,
  for example) gets no AA check, so a clone is approved on a flow without a required level. It cannot
  reach substantial. (Found 2026-10-06.)
- [ ] **Raising a customer's retention after a session** moves the wallet's purge, not the engine copy's
  retention; the subject was told the old number at consent. (Found 2026-10-06.)
- [ ] **`SecretReveal` passes `dismissible={false}`** without a render test (the frontend tests have no
  DOM).
- [ ] **An identity session in `needs_review`** has no purge time (`setPurgeAt` covers data requests);
  moot until the review trigger exists.
- [ ] **App long-poll.** `GET /app/{token}/events` re-reads the session from the store every 500 ms
  (`SessionEventsPoll`) while a client waits; a change notice would spare those reads.

## The user's own list

- [ ] Driving licence, see go-passport-issuer (above).
- [ ] Make the Yivi route work (above).
- [ ] Who sees what: org member, org admin, platform admin, customer.
- [ ] White labelling: start in the wallet settings per org, then per customer, and carry it through to
  the QR code into the Idem app. Tie the API key to org and customer, so a white label can only be used
  by the right party.
- [ ] API security: can the API be spammed, and how to prevent it; limit what can be sent, and send it
  once.
- [ ] **Rate limits as an org budget with customers inside it.** Today every customer has its own
  fixed bucket (`apiCallLimit` 120/min, `APISessionLimit` 10/min, `hostedCallLimit` 3000/min) and an org
  has no total, so an org with ten customers gets ten times the API.
  - Two levels, both must have room for a call to pass:
    - the customer's bucket (a hosted page: its link's), so one customer cannot take everything;
    - the org's bucket: all its customers' calls plus its own member identity proofing.
  - Step 1: both levels with fixed defaults in code, still in-process (`internal/ratelimit`); no
    migration.
  - Step 2: an org admin sets a lower limit per customer, within the org's budget (a customer setting,
    audited).
  - Optional: `RateLimit-Limit` / `-Remaining` / `-Reset` headers on every API response, not only
    `Retry-After` on a 429.
  - Still per replica (in-memory); a shared counter only once the API runs on several replicas.
- [ ] **Handover on the hosted link.** Later; the on-screen page is done.
  - On-screen (`IdemOnScreen`) works: it follows the app (`waiting` / `connected` / `away`), hides
    the QR while the app holds the session and shows a handover QR once the app is `away`.
  - Hosted (`/p/:token`) does not: the session QR stays shown after the app took the session, because
    the page shows `manualIdem` and does not follow the app.
  - The handover itself stays `POST /proof/{token}/claim-link` (audited as `hosted_link`); R14 above
    still holds: no handover from inside the Idem app.
- [ ] **audit log filter and export.** later, buttons are in place, function for it not yet.
- [ ] **API usage per org, ranked.** An org admin sees who uses the org's budget, most usage at the top:
  each customer, and the org's own member identity proofing. Pure idea, nothing decided:
  - per customer the calls, sessions created and 429s over a period (today, 7 days, 30 days);
  - per API key under its customer, so a misbehaving integration is found;
  - needs usage counted somewhere durable: the in-memory buckets keep nothing.

## Before launch

- [ ] **Controller chain.** The design assumes the customer is controller, the org processor and Yivi
  sub-processor; that needs a DPA template per customer before the first live key. Self-hosted, the
  operating org is processor and Yivi sub-processor only for the shared engine; SaaS, Yivi hosts the
  wallet and is sub-processor for all of it.
- [ ] **Regula licence and capacity.** Does the licence cover third-party customers, at what volume, and
  does the wallet get its own Regula instance?
- [ ] **`high` assurance.** Only with a certified liveness vendor; is there a customer that needs it?
- [ ] **Trust lists.** The EU trusted lists' XAdES signature is not verified (no mature Go library), and a
  stale cache is used when a fetch fails, as in go-diploma-issuer.
- [ ] **The Idem app.** Confirm the deep link opens the app on both platforms and that it only uses the
  step endpoints (the single-shot `/result` is refused).

## Process

- [x] Dobby's `CHANGES_REQUESTED` (not a draft PR, non-atomic commits and messages) is process only and
  gets dismissed; the PR is not split and its history stays as it is.
- [x] Dobby's 2026-10-06 minimal-diff finding (the held-credential re-check and history in their own PR)
  falls under the same decision: they stay in this PR.

## Harvest

- Feature doc: `.ai/features/identity-proofing.md`, updated to the code as it is.
- Conventions: none.
