# Feature: OpenID4VP presentation requests over QERDS

**Status:** receive side implemented (issue
[#271](https://github.com/privacybydesign/yivi-businesswallet/issues/271)): an
organization can send another organization's business wallet an OpenID4VP
Authorization Request over QERDS instead of a browser redirect, and it is
validated and queued exactly where a browser-driven organization selection
already leaves one — `StatusOrgSelected`, waiting on the governance layer
(#113). Neither the sender side (building an arbitrary-vct DCQL query for one
org to ask another) nor an approve/decide action is built here — see §5.
Companion to `.ai/features/oid4vci-over-qerds.md` (the same
QERDS-carries-the-invitation shape, issuance direction) and
`.ai/features/openid4vp-inbound.md` (the presentation crypto and HTTP
invocation this reuses, #112/#188).

---

## 1. Why this is not #112 again

#112/#188 already let an **external verifier** invoke the business wallet over
HTTPS: a browser navigates to `/openid4vp?client_id=…&request_uri=…`, a human
authenticates and picks an organization, and `eudiholder.Engine.Present`
answers with whatever the DCQL query asked for — that machinery does not care
what vct it is asked about. What it cannot do is originate from a **QERDS
message** instead of a browser: there the receiving organization is already
known from the digital address the message arrived on (no human to pick one),
and the request may sit in a queue for hours before anyone looks at it, which
the browser flow's 5-minute `OPENID4VP_TRANSACTION_TTL` was never shaped for.
This feature is that second invocation seam, reusing #112's validation and
matching machinery unchanged, and stopping exactly where the browser flow
already stops without a governance decision: `StatusOrgSelected`.

## 2. What is built

| Piece | Where |
|---|---|
| QERDS envelope (`type: vp-presentation-request/v1`) | `openid4vppresenter.MarshalPresentationRequestEnvelope` / `ParsePresentationRequestEnvelope` (`presentationrequest_envelope.go`) |
| Inbound consumer | `openid4vppresenter.Receiver`, wired into `qerds.Service` alongside `attestation.OfferReceiver` (`receiver.go`, `cmd/api/main.go`'s `chainedInboundConsumer`) |
| Queued, org-bound request | `Service.ReceiveFromQERDS` → `Store.CreateForOrganization`, landing at `StatusOrgSelected` directly (skips `pending_auth`/org-picker) |
| Idempotency | `openid4vp_transactions.source_message_id`, unique per organization (partial index, migration `20260925090000`) — a re-delivered message resolves to the row already queued, whatever it decided |
| Audit | `audit.PresentationRequestReceived`, written once per newly queued row (never on a re-delivery) |

Deciding a queued row — listing an organization's `org_selected` transactions,
approving one (present + respond) or declining one — is **not** duplicated
here. `Service.Select`'s own auto-present branch already shows the shape that
decision takes (`s.present`, unchanged by this feature); whatever surface an
admin uses to invoke it for a browser-selected organization applies unchanged
to a QERDS-queued one, since both are the same `org_selected` row in the same
table with no discriminating field an approval UI would need to branch on.

## 3. Flow

```
 org A (sender)                      QERDS (AS4)                    org B (receiver)
      │ mint client_id + request_uri                                       │
      │ at *some* OpenID4VP verifier                                       │
      │──── vp-presentation-request/v1 envelope ─────────────────────────▶│
      │                                                    qerds.Service.PollAll
      │                                                    → Receiver.OnInboundMessage
      │                                                    → fetch + validate request_uri
      │                                                      (openid4vppresenter.Validator,
      │                                                       same as the browser path)
      │                                                    → queued at org_selected,
      │                                                      bound to org B already
      │                                        (governance decision — #113, not this seam)
      │◀──────────────────────────── direct_post (vp_token, state), once decided ─│
```

The request travels over QERDS; the response, once approved, goes straight
from B to the verifier's `response_uri` over HTTPS, exactly as it does for the
browser flow (`.ai/features/openid4vp-inbound.md` §2). QERDS therefore proves
that A *sent* the invocation, not what B disclosed in answer to it — the same
asymmetry `oid4vci-over-qerds.md` §1 notes for the credential-offer direction.

## 4. Reused, not duplicated

Everything downstream of "is this request_uri worth queuing" is #112's
existing code, called through the same seam `Service.Start` already used for a
browser invocation:

- `Service.validate` (the fetch + `Validator.Validate` pair) is shared between
  `Start` (browser) and `ReceiveFromQERDS` (QERDS) — a QERDS-carried request is
  refused by exactly the same rules (SSRF/redirect/size guards, signed-JAR
  verification, `checkFields`) as one a verifier redirected a browser to.
- The `openid4vp_transactions` table gained one nullable column
  (`source_message_id`) rather than a parallel table: a QERDS-originated row is
  the same state machine, just entered at a different point (`org_selected`,
  with the organization already known instead of picked by a logged-in member).
- Nothing about `s.present` (DCQL match, `direct_post`/`direct_post.jwt`,
  one-time-use consumption) changes: a QERDS-originated row reaches it through
  whatever decides any `org_selected` transaction, not a QERDS-specific path.

## 5. What is deliberately not here

- **An approve/decline action.** Adding one here would fork #113's still-open
  question of *who* may let an organization's disclosure go out into a
  QERDS-specific copy of it. A queued row is indistinguishable from a
  browser-selected one once it reaches `org_selected` (§4), so whatever surface
  eventually decides that state generically also decides this one — nothing
  QERDS-specific to build or keep in sync.
- **Arbitrary DCQL queries for the sender** (the issue's item 1). Today
  nothing in this repo can *build* a presentation request asking for an
  org-held credential type — `internal/openid4vpverifier` only knows the
  hardcoded personal-credential scopes (login/identity/vog) it uses against
  the hosted Yivi verifier for natural-person disclosure, an unrelated flow.
  Building that query for org-to-org use is unsafe before each org has its own
  relying-party identity: every org shares one `client_id` today, so the
  verifier's signed request carries Yivi's identity, not the requesting org's,
  and a receiving org cannot tell "org A asked" from "org A relayed a request
  minted by org C" (issue #271, "Security: relay risk"). This feature's
  receive side does not depend on that work: it validates and queues whatever
  a QERDS-delivered invocation asks for, from any relying party, the same way
  the browser path already does for an external verifier.
- **Console UI.** Nothing in `frontend/` lists a queued request yet — that
  belongs with whatever surfaces the `org_selected` queue generically (§5's
  first point), not a QERDS-specific list.

## 6. Testing

Unit (`openid4vppresenter/*_test.go`): envelope marshal/round-trip and the
"not a presentation request" cases; `Service.ReceiveFromQERDS` rejects the
same ambiguous/unsupported invocation forms as `Start` and is idempotent on
the source message; `Receiver` queues, ignores non-request bodies, is
idempotent on re-delivery (including once a row has moved past `org_selected`),
and swallows (logs, does not error) a QERDS message whose invocation the
validator refuses.

In-process end-to-end (`qerds_e2e_test.go`, no database): the real
`qerds.Service` carries the envelope between two in-memory organizations and
`Receiver` queues it org-bound, proving the wiring `cmd/api/main.go` assembles
— same shape as `attestation`'s `offer_qerds_e2e_test.go` for the issuance
direction.

**Not yet exercised:** a real signed Request Object end to end (the unit tests
use the same fakes `openid4vppresenter`'s browser-flow tests use for the
fetcher/validator seams — the `VerifyingValidator` combination is already
covered by `openid4vp-inbound.md` §8's bench and integration tests, unchanged
by this feature) and a live approve/decline of a QERDS-queued row (depends on
whatever surface #113 lands for `org_selected` transactions generally).
