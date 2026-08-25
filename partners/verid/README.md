# Sending credential offers to a Yivi Business Wallet over QERDS / AS4

**Audience:** an engineer at ver.id integrating with the Yivi Business Wallet.
**Status:** proof of concept — the addressing and payload below are stable enough
to build against; see [Limitations](#limitations).

You issue an attestation (EAA) to an organization that holds a Yivi Business
Wallet. This document is how you deliver the **OpenID4VCI credential offer** to
that organization so its wallet fetches the credential automatically — over
**QERDS**: qualified electronic registered delivery, carried on **AS4/eDelivery**.

The channel is deliberately **send-only**. You push offers to the wallet; the
wallet never pushes to you. That is enforced in both gateways' PModes, not by
convention — see [Why send-only is easier](#why-send-only-is-easier-than-you-might-expect).

## Why QERDS and not a REST endpoint

Because the delivery evidence is the product, not a side effect. eIDAS Art 44
requires a qualified registered delivery service to guarantee, with legal effect:
identification of sender and recipient, integrity, a qualified electronic
timestamp on send and on receive, and tamper-evident evidence of both. An
authenticated HTTPS POST gives none of that.

For the business wallet this is a regulatory obligation (COM(2025) 838 Art
5(1)(i), 5(1)(m), 5(1)(n)), not a preference. So the transport is AS4 and the
evidence chain is part of what you are integrating with.

## What you need to operate

An **AS4 access point**: the component that terminates AS4/ebMS3, holds your
keypair and truststore, signs and encrypts messages, and gives your application a
backend interface to submit through. It carries no business logic — it is a
protocol gateway.

The eDelivery **AS4 profile** is the contract; Domibus is one implementation of
it, not a requirement:

| Option | Effort | Notes |
|---|---|---|
| **Hosted access point at a QTSP** | none (commercial) | The only route that yields genuinely *qualified* evidence. Recommended if the evidence must hold up legally |
| **phase4** (Java library) | lowest self-hosted | A conformant sender in tens of lines. Good fit for send-only |
| **Holodeck B2B** | low | File-based submission, no database needed |
| **Domibus** | highest | The EC reference implementation. Worth it if you will eventually receive too, or want to mirror the wallet's own stack |

Check **eDelivery-profile** conformance specifically. A gateway that "supports
AS4" for PEPPOL is not automatically conformant to the eDelivery AS4 profile.

## Why send-only is easier than you might expect

The reliability configuration on both sides is `replyPattern="response"`, so the
AS4 **Receipt** (non-repudiation of receipt) comes back on the HTTP response to
your own outbound POST. Consequences for you:

- **no publicly reachable inbound endpoint**
- **no MSH TLS server certificate**
- **no DMZ or inbound firewall rule**
- **no receive-side plugin configuration**

You make outbound HTTPS calls and get your evidence on the response. Your party
entry in the wallet's PMode still needs an `endpoint` attribute because the schema
requires one, but it is never dereferenced.

## Onboarding (one-time)

### 1. Generate your AS4 keypair

Your gateway signs every message with this key. The wallet verifies against the
certificate you send us.

```sh
keytool -genkeypair -alias verid_gw -keyalg RSA -keysize 2048 -sigalg SHA256withRSA \
  -validity 3650 -dname "CN=verid_gw, O=Ver.ID, C=NL" \
  -keystore verid_keystore.jks -storepass CHANGEME -keypass CHANGEME -storetype JKS
keytool -exportcert -alias verid_gw -keystore verid_keystore.jks -storepass CHANGEME -rfc -file verid_gw.cer
```

For anything beyond a bench this must be a certificate chaining to a CA the
eDelivery network recognizes — not self-signed.

### 2. Exchange certificates and identifiers

Send the wallet operator:

- `verid_gw.cer` — your AS4 signing certificate
- your **party id** and its **id type**. Use a registered scheme, e.g. ISO 6523
  ICD `0106` (Dutch KVK): `0106:<your KVK number>`. The bench uses the
  `unregistered` URN, which is fine for a loopback and meaningless to anyone else
- your **QERDS digital address** (`originalSender`), e.g. `verid@<your domain>`.
  Note that both this and the party id above are allowlisted on the wallet side
  before an offer is auto-redeemed — see
  [Two separate trust decisions](#two-separate-trust-decisions)
- your issuer's **trust anchor / certificate chain** for the credentials you will
  issue (see [Two separate trust decisions](#two-separate-trust-decisions))

You receive:

- the wallet gateway's AS4 certificate, for your truststore
- the wallet gateway's **party id** and **MSH endpoint URL**
- the ebMS3 service/action to use, if it differs from the bench values below

### 3. Configure your PMode

[`../../docker/development/domibus/pmode-verid.xml`](../../docker/development/domibus/pmode-verid.xml)
is a working reference — it is the PMode the bench's simulated ver.id gateway
actually runs. Adapt the party ids, endpoints and certificates; keep everything
under `<businessProcesses>` that is not party-specific **byte-identical** to the
wallet's PMode. AS4 legs only match when both gateways agree on service, action,
leg, security policy and property set.

### 4. Learn each recipient's digital address

Offers are addressed to an organization's **QERDS digital address**, not to a
company name or KVK number. It looks like an email address —
`acme@qerds.example` — and derives from the organization's wallet slug.

There is no directory lookup yet: the European Digital Directory (COM(2025) 838
Art 10, resolved via SMP/SML) does not exist yet, so addresses are exchanged out
of band. When it lands it becomes another resolver behind the same seam and this
step disappears.

## Sending an offer

### ebMS3 addressing

Submit through your own access point with:

| Field | Value (bench) | Notes |
|---|---|---|
| `From` PartyId | `verid` | your party; a real deployment uses `0106:<KVK>` |
| `To` PartyId | `domibus-blue` | the wallet gateway's party |
| PartyId type | `urn:oasis:names:tc:ebcore:partyid-type:unregistered` | replace with the registered scheme |
| `Service` | `bdx:noprocess` (type `tc1`) | must match the wallet's PMode |
| `Action` | `TC1Leg1` | must match the wallet's PMode |
| MEP / binding | one-way / push | |

### Message properties

These are required by the PMode property set. **`finalRecipient` is how the
wallet decides which organization receives the offer** — get it wrong and the
message is either rejected as unknown or delivered to the wrong wallet.

| Property | Required | Value |
|---|---|---|
| `originalSender` | yes | your QERDS digital address, e.g. `verid@partners.example` |
| `finalRecipient` | yes | the receiving organization's QERDS digital address |
| `subject` | no | inbox subject line |

### Payload

One payload part, content id **`cid:message`**, MIME type `text/plain`, carrying
this JSON:

```json
{
  "type": "eaa-credential-offer/v1",
  "senderOrgName": "Ver.ID",
  "credentialName": "Bewijs van inschrijving",
  "credentialOffer": "openid-credential-offer://?credential_offer=%7B%22credential_issuer%22%3A%22https%3A%2F%2Fissuer.ver.id%22%7D",
  "message": "Ver.ID has offered your organization a credential (Bewijs van inschrijving). Your business wallet adds it automatically."
}
```

- **`type`** must be exactly `eaa-credential-offer/v1`. The wallet uses it to tell
  a machine-consumable offer from an ordinary human message; anything else is
  filed as a normal QERDS message and never redeemed.
- **`credentialOffer`** is the OpenID4VCI offer: an `openid-credential-offer://`
  deeplink (by value) or an `https://` credential offer URI (by reference).
  Self-contained — it encodes your credential issuer, the configuration id and
  the one-time pre-authorized code.
- **`message`** is a human-readable fallback for inboxes that show the body to an
  operator instead of auto-redeeming.

Canonical definition: [`internal/attestation/offer_envelope.go`](../../backend/internal/attestation/offer_envelope.go).

### Use the pre-authorized code grant

Mint the offer with
`grants["urn:ietf:params:oauth:grant-type:pre-authorized_code"]`. The receiving
wallet is **headless** — there is no browser and no operator at redemption time —
so it declines `authorization_code` offers outright.

**Do not set `tx_code`.** Its purpose is to stop an intercepted QR being redeemed
by an attacker; over an authenticated QERDS channel it is redundant, and a code
the receiving automation cannot obtain simply blocks issuance.

### Reference implementation

[`backend/cmd/as4offer`](../../backend/cmd/as4offer) submits exactly this message
through a Domibus WS plugin. It is the driver for the bench below and doubles as
a reference for the sending side — it reuses the wallet's own envelope marshaller,
so what it sends cannot drift from what the wallet parses.

```sh
cd backend
go run ./cmd/as4offer \
  -endpoint http://localhost:8091/domibus/services/backend \
  -recipient acme@qerds.localhost \
  -name 'Bewijs van inschrijving' \
  -offer 'openid-credential-offer://?credential_offer=%7B...%7D'
```

## What happens on receipt

1. The wallet's Domibus validates your AS4 signature against its truststore and
   returns the AS4 receipt on your HTTP response.
2. A background poller drains the message and resolves the organization from
   `finalRecipient`.
3. The offer envelope is recognised, and the organization's wallet redeems it
   against **your** OpenID4VCI token and credential endpoints, server-to-server.
4. The received SD-JWT VC is validated against the wallet's trusted-issuer chain
   and stored.

Two things follow. **Your issuer endpoints must be reachable** from the wallet
backend over HTTPS — QERDS carries the offer, not the credential. And **the
pre-authorized code must still be valid** when step 3 runs, so keep its lifetime
comfortably longer than a poll interval.

## Two separate trust decisions

Worth being explicit, because these are easy to conflate:

| Question | Answered by |
|---|---|
| Who delivered this offer? | your **AS4 signature**, plus the wallet-side allowlists below |
| Is the credential authentic? | the **SD-JWT VC issuer chain**, validated by the receiving wallet |

Being registered as an AS4 party authenticates you as a *sender*. It does not
authorize your *issuer*. Both must be configured, and they are different
identities — a party registration without the issuer trust anchor gets you
accepted messages whose redemption then fails.

Where each one lands on the wallet side:

| Trust decision | Where it is configured |
|---|---|
| AS4 sender | your gateway certificate in the wallet's Domibus truststore, plus your digital address in `QERDS_TRUSTED_OFFER_SENDERS` |
| Credential issuer | your root CA in `ATTESTATION_HOLDER_TRUST_CHAIN` |

`ATTESTATION_HOLDER_TRUST_CHAIN` takes the PEM itself, not a path, and *adds* to
the anchors the wallet already trusts rather than replacing them — so several
partners can be trusted at once by concatenating their roots. Self-signed
certificates are taken as roots and everything else as intermediates, so the
order does not matter. Your current dev root is checked in at
[`verid-dev-root-ca.crt`](verid-dev-root-ca.crt):

```
CN=Ver.iD Dev Root CA, O=Subst.id B.V., C=NL
SHA-256 02:4C:01:51:EE:C4:AC:7D:CB:D8:01:0B:15:B7:87:73:74:93:F5:23:9A:64:BE:BF:EE:89:26:9B:39:9A:7D:8B
valid until 2035-06-30
```

Send us the **full chain** if your issuer signs from an intermediate rather than
straight off that root — the root alone cannot complete the path.

The reason the sender side matters at all: a credential offer is a **bearer
token**. Whoever redeems the pre-authorized code gets the credential, so a
genuine offer replayed at a different organization would produce a correctly
signed, correctly chained credential in the wrong wallet. Content validation
cannot catch that; transport identity can.

Which is why the wallet side allowlists **your party id**, not just your address:

| Wallet setting | Matched against | Who controls the value |
|---|---|---|
| `QERDS_TRUSTED_OFFER_PARTIES` | your ebMS3 `From` PartyId | the wallet's gateway, which checks it against its PMode and your signing certificate |
| `QERDS_TRUSTED_OFFER_SENDERS` | your `originalSender` address | you — it is a message property, so any admitted party can write any value in it |

Both are checked, so give the wallet operator both values in
[step 2](#2-exchange-certificates-and-identifiers) and keep them stable: rotating
your party id without telling them stops offers being redeemed (they are still
stored, and the backend logs `credential offer from untrusted sender not
redeemed`). An offer that fails either check is not rejected or lost — it lands in
the organization's inbox and waits for a human.

## Trying it locally

The wallet repo ships a **two-gateway bench**: a second, independent Domibus
standing in for your access point, with its own key, so offers arrive from a
genuinely foreign party over a real cross-gateway AS4 leg.

```sh
docker compose --profile domibus --profile verid up -d   # slow on first boot
```

Set the wallet-side trust gate to the bench's identities (in `.env`, or the shell
you run compose in) — otherwise the backend trusts every party and every sender,
warns about it at boot, and you are not exercising the gate at all:

```sh
QERDS_TRUSTED_OFFER_PARTIES=verid                          # the ebMS3 From PartyId
QERDS_TRUSTED_OFFER_SENDERS=verid@partners.qerds.localhost  # the originalSender address
```

- your simulated gateway's admin console: <http://localhost:8091/domibus> (`admin` / `123456`)
- the wallet's gateway: <http://localhost:8090/domibus>

Then submit with `cmd/as4offer` as above. Details and verification steps:
[`.ai/features/qerds.md`](../../.ai/features/qerds.md).

## Limitations

At this proof-of-concept stage:

1. **Not qualified.** The bench proves AS4 transport plumbing. The certificates
   are self-signed and no QTSP is involved, so there are no qualified timestamps
   and no qualified evidence. Real qualified delivery needs a partner QTSP on at
   least one side.
2. **No delivery callback beyond the AS4 receipt.** You learn that the wallet's
   gateway accepted the message. Whether the credential was successfully issued
   is visible at your own token endpoint, not reported back over QERDS.
3. **A failed redemption is not retried.** The message stays in the
   organization's inbox for follow-up. Re-send if a re-issue is needed.
4. **No directory.** Recipient addresses are exchanged out of band (see step 4).
