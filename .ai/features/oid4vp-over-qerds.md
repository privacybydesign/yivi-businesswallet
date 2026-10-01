# Feature: OpenID4VP presentation requests over QERDS

**Status:** implemented both ways (issue
[#271](https://github.com/privacybydesign/yivi-businesswallet/issues/271)). An
organization asks another organization's business wallet for credentials: it
signs an OpenID4VP Authorization Request under **its own certified identity**,
sends the invocation over QERDS, the receiving organization's admin approves or
declines it in the console, and the answer comes back to the requester's
`response_uri`, where it is verified and shown. Companion to
`.ai/features/oid4vci-over-qerds.md` (the same QERDS-carries-the-invitation
shape, issuance direction) and `.ai/features/openid4vp-inbound.md` (the
presentation crypto, the browser invocation and the approval queue this reuses,
#112/#188/#113).

---

## 1. Why this is not #112 again

#112/#188 already let an **external verifier** invoke the business wallet over
HTTPS: a browser navigates to `/openid4vp?client_id=…&request_uri=…`, a human
authenticates and picks an organization, and `eudiholder.Engine.Present`
answers with whatever the DCQL query asked for. Two things were missing for
org-to-org:

- **An invocation that is not a browser.** Over QERDS the receiving
  organization is already known from the digital address the message arrived
  on, and the request waits in a queue until an admin looks at it — hours or
  days, not the browser flow's 5-minute `OPENID4VP_TRANSACTION_TTL`.
- **A requester with its own identity.** Nothing could *build* a request for an
  org-held credential type, and every org shared one relying-party identity, so
  a receiver could not tell "A asked" from "A relayed a request minted by C"
  (the issue's relay risk). §3 is the answer to that.

## 2. What is built

| Piece | Where |
|---|---|
| Shared relying-party crypto (JAR signing, DCQL builder, vp_token verification) | `internal/relyingparty` (moved out of `internal/devverifier`, which keeps only the dev identity) |
| Requester CA + per-request organization certificate | `relyingparty.CA` (`IssueOrganization`, `X509HashClientID`), loaded/minted by `newRequesterCA` in `cmd/api/main.go` |
| Sender slice | `internal/openid4vprequester` (store → service → handler), table `openid4vp_outbound_requests` (migration `20260930083305`) |
| QERDS envelope (`type: vp-presentation-request/v1`) | `openid4vppresenter.MarshalPresentationRequestEnvelope` / `ParsePresentationRequestEnvelope` |
| Inbound consumer | `openid4vppresenter.Receiver` → `Service.ReceiveFromQERDS(orgID, messageID, sender, …)` → `Store.CreateForOrganization` at `org_selected` |
| Sender binding at receipt | `RequestObject.CertifiedName` / `CertifiedAddresses` (set by `VerifyingValidator` from the verified leaf), checked in `ReceiveFromQERDS` |
| Approval | the #113 queue, unchanged: `GET/POST /orgs/{slug}/openid4vp/requests…` |
| Idempotency | `openid4vp_transactions.source_message_id`, unique per organization |
| Audit | receive: `presentation.request_received`; send: `presentation.request_sent`, `presentation.response_received` (verified answer), `presentation.request_failed` (refused answer or undelivered request) (target `outbound_presentation_request`) — types and outcome only, never claim values |
| Console | `/:orgSlug/credential-requests` (`frontend/src/routes/credential-requests.tsx`): incoming inbox (approve/decline), sent list with the verified disclosure, request form. Admin-only; the sidebar item shows for admins |

**What can be asked for.** The request form picks from the trust scheme's
catalogue: every active or deprecated credential schema any issuing
organisation on this deployment designed (`attestation.Store.ListSchemaCatalog`,
drafts excluded), grouped by issuer, with the schema's attributes as
checkboxes: none ticked asks only that the credential is held. An *Other
credential type* row keeps a typed vct and claim names for issuers outside the
deployment (e.g. the KVK registration). The API itself still takes any vct.

Routes of the sender slice:

| Route | Who |
|---|---|
| `POST /orgs/{slug}/openid4vp/outbound` `{from?, recipient, credentials:[{vct, claims}]}` | admin |
| `GET /orgs/{slug}/openid4vp/outbound`, `GET …/outbound/{id}` | admin |
| `GET /orgs/{slug}/openid4vp/credential-types` (the trust scheme's catalogue) | admin |
| `GET /openid4vp/outbound/{id}/request-object` (request_uri) | public — the receiving wallet |
| `POST /openid4vp/outbound/{id}/response` (response_uri, `direct_post`) | public — the receiving wallet |

## 3. Identity and the relay binding

- **One certificate per request.** `Service.Send` resolves the sending QERDS
  address first (`qerds.Service.ResolveSender`, exported for this), then has the
  requester CA mint a leaf: subject O/CN = the organization's name, **the only
  SAN = that address (rfc822Name)**, digitalSignature + clientAuth, lifetime =
  `OPENID4VP_ORG_REQUEST_TTL`. The leaf key signs the JAR and is dropped;
  nothing per-org is stored or rotated.
- **`client_id = x509_hash:<b64url sha256(leaf DER)>`.** irmago's
  `RequestorCertificateStoreVerifierValidator` supports it and, unlike
  `x509_san_dns`, it needs no DNS name per organization. The KB-JWT's `aud` is
  this client_id, which is what the response check binds to.
- **The receiver binds the request to the QERDS sender.** `ReceiveFromQERDS`
  refuses (`ErrInvalidRequestObject`, logged by `Receiver`, not queued) unless
  `Inbound.Sender` (originalSender) is one of the verified leaf's email SANs,
  case-insensitively. A request C signed and A forwarded names C's address, not
  A's, so it is refused instead of queuing under A's name. The queued row's
  `VerifierIdentity` is then the **certified** name, not the self-asserted
  `client_metadata.client_name` (which irmago would otherwise prefer).
- **Trust.** A deployment appends its own requester CA root to the verifier
  trust, so its organizations' requests to each other verify with no
  configuration. Another deployment trusts ours by adding our CA certificate to
  its `OPENID4VP_VERIFIER_TRUST_CHAIN`; the originalSender is then as
  trustworthy as that deployment, the same trust the CA already expresses.
- **Consequence:** under `OPENID4VP_PRESENTER_ALLOW_UNVERIFIED_REQUEST_OBJECTS`
  every QERDS request is refused — only a verified certificate can certify a
  sender. The boot warning says so. A relying party whose certificate carries
  no email SAN (e.g. the hosted Yivi verifier) can still use the browser flow,
  just not QERDS.

## 4. Flow

```
 org A (requester)                         QERDS                     org B (holder)
 POST …/openid4vp/outbound
  ResolveSender → CA.IssueOrganization(name, address)
  sign JAR (x509_hash, direct_post, nonce, state, DCQL)
  store row (sent) ──── vp-presentation-request/v1 envelope ──────▶ Receiver
                                                                    GET request_uri ──┐
  FetchRequestObject (once) ◀──────────────────────────────────────────────────────────┘
                                                                    verify chain + bind sender
                                                                    queue at org_selected
                                                                    admin: approve / decline
  POST response_uri ◀──────────────── direct_post (vp_token, state) ─ Present (approve only)
  state ≠ row → 400, row untouched
  verify each presentation (issuer trust, KB-JWT nonce/aud, vct) → completed | failed
```

- **request_uri is single-fetch** (`FetchRequestObject` is one guarded
  UPDATE). A re-delivered QERDS message fetches again, fails, and is dropped as
  an unactionable invocation — the row from the first delivery stands.
- **response_uri is authenticated by `state`.** The id travels in the QERDS
  message; `state` only inside the signed JAR. A post with the wrong state is
  refused and changes nothing, so nobody can burn a request by posting to its
  URL. A post with the right state is the holder's one answer: every requested
  credential must be present exactly once, verify
  (`relyingparty.VerifyVPToken` against the same issuer trust the holder uses —
  `ATTESTATION_HOLDER_TRUST_CHAIN` + pinned Yivi anchors) and be the requested
  vct; otherwise the row becomes `failed` (`verification_failed` /
  `incomplete_response`) and nothing it carried is kept.
- The response goes straight over HTTPS, not QERDS: QERDS proves A *sent* the
  request, not what B disclosed.

## 5. Config

| Variable | Default | Meaning |
|---|---|---|
| `OPENID4VP_ORG_REQUEST_TTL` | `168h` | Sender: request, JAR `exp`, leaf lifetime, response window. Receiver: how long a QERDS-queued request waits for approval (`openid4vppresenter.Store`'s `qerdsTTL`) |
| `OPENID4VP_REQUESTER_CA_CERT` / `_KEY` | empty | PEM CA certificate + EC key (PKCS#8 or SEC 1), both or neither. Empty mints an ephemeral CA at boot (warning): only this deployment trusts it, and a request signed before a restart no longer verifies after it |
| `OPENID4VP_REQUESTER_PUBLIC_URL` | `APP_BASE_URL` | Base URL (no `/api/v1`) the other wallet fetches request_uri from and posts to. The dev stack sets `http://backend:8080`, since the "other wallet" is the same container |

## 6. Testing

- **End to end, no database** (`openid4vprequester/e2e_test.go`): A's service
  signs and sends over the real `qerds.Service`; B's `Receiver` fetches the
  Request Object over HTTP from A's handler, verifies it with the production
  `VerifyingValidator` against A's CA root, binds the sender and queues it under
  A's certified name; B's `Approve` posts the stub holder's answer to A's
  response endpoint. Only the issuer-signature/KB-JWT check is faked (the stub
  holder's token cannot pass it; `relyingparty`'s own tests cover it).
- Unit: the relay refusal and case-insensitive match
  (`openid4vppresenter/service_test.go`); a CA-issued identity verifying with
  its certified fields, and another CA's not
  (`verifyingvalidator_test.go`); send validation, undelivered → failed, state
  handling, refused answers, second answer, expiry
  (`openid4vprequester/service_test.go`).
- Integration (`openid4vprequester/store_integration_test.go`): per-org
  scoping, single fetch, settle-once, expiry, audit trail.
- Frontend: `lib/credential-request.test.ts` (form parsing/validation).

**Locally, by hand:** two orgs on the dev stack, `ATTESTATION_HOLDER=irmago` and
a credential held by org B (see `openid4vp-inbound.md` §8 step 1). As an admin of
A open *Credential requests → Request credentials*, enter B's QERDS address and
the held vct; the request shows in B's *Incoming* tab after the next QERDS poll.

## 7. Open

- **A decline is not reported back.** `Deny` sends nothing, so A's request stays
  `sent` until it expires. OpenID4VP allows an error response
  (`error=access_denied`) to the response_uri; B could post one on decline and A
  could record it as `declined`.
- **`direct_post.jwt` for outbound requests.** It would need the encryption
  private key held (encrypted at rest) for the whole request lifetime.
- The approval permission is still plain `admin` (see `openid4vp-inbound.md`
  §9); no auto-approve policy, no four-eyes.
- Claims are top-level names only; nested claim paths and `claim_sets` are not
  expressible from the form or the API.
