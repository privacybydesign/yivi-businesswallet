# Signing with Yivi: the wallet's own CSC signing service, and the signing API

Status: **implemented** (backend + frontend). Builds on the signing ceremony
(`.ai/features/signing-ceremony.md`), which is unchanged: it is still the client of a
CSC API v2 provider. What is new is that the wallet now *is* one, so signing works
on a deployment with no remote QTSP, and that an organisation's own software can
drive signing over an API.

## Why

The ceremony was only ever exercised against the EU reference QTSP
(`--profile signer`), whose authorization step asks an mdoc PID from the EUDI
reference wallet. Nobody in production holds one, and Yivi cannot present it, so
signing did not work in production at all. Yivi can identify a person today, over
the hosted OpenID4VP verifier the login already uses. So the wallet hosts a signing
provider whose authorization step is that.

## What it is, and what it is not

`internal/yivicsc` is a Cloud Signature Consortium API v2 provider plus the OAuth 2.0
authorization server in front of it, mounted at `/api/v1/yivi-csc`:
`csc/v2/info`, `oauth2/authorize`, `oauth2/token`, `csc/v2/credentials/list`,
`csc/v2/credentials/info`, `csc/v2/signatures/signHash`.

- **A signer is whoever a Yivi disclosure names.** On authorize the service starts a
  presentation at the verifier (`openid4vpverifier.StartSigning`) for the given names,
  family name and date of birth, from a passport, identity card or residence permit
  (`mrtd`) or from the registered personal data, `pbdf.gemeente.personalData` (`brp`).
  They are options of one DCQL credential set, so the signer picks in the Yivi app.
  Nothing else is asked: no nationality, photo or BSN.
- **Not qualified.** Keys are P-256 keys in Postgres, sealed under
  `CSC_ENCRYPTION_KEY`; certificates chain to a certificate authority the deployment
  made for itself (`yivi_csc_authority`, one row, made on first boot). No QSCD, no
  qualified certificate, no trusted list: a signature made here is an **advanced**
  electronic signature whose signer was identified with Yivi. A PDF viewer reports a
  valid signature from an unknown issuer, exactly as it did with the demo QTSP. The
  signature dictionary's reason is therefore the neutral "Electronic signature"
  (`signatureReason`), for every provider.

## The two legs, on this provider

The ceremony's two-ceremony model stands (the certificate must be known before the
document hash exists), so a signer scans twice the first time and once per document
after.

1. **Link** (`scope=service`). The disclosure offers every source. Its token's
   `credentials/list` finds the person's credential or issues one: a key and a
   certificate with their name as `CN` (plus `givenName` and `surname`), valid a year
   (`CredentialValidity`), usable for signatures only.
2. **Sign** (`scope=credential`, bound to `credentialID` + `hashes`). The disclosure
   offers only the **family the certificate was issued on**, and the person disclosed
   must be the certificate's subject, else the authorization is denied
   (`identity_mismatch`). That is the service's sole-control check: holding a
   credential id is worth nothing without that person's Yivi app. The access token is
   the signature activation data: `signHash` signs only the digests it was bound to,
   each once.

- **Why one family per certificate.** A chip writes a name as the MRZ has it
  (upper case, transliterated), the population register as the person spells it. The
  two do not compare, so a certificate issued on one is never unlocked by the other;
  the authorization page tells the signer which to use (`source`).
- **Who a person is.** `subject_key` is `HMAC-SHA256` over the family, the folded
  names and the date of birth, keyed with a secret derived from the CSC key
  (`crypto.Cipher.DeriveSecret`). The row holds no date of birth, and a plain hash
  would have given it away to anyone trying every day of a century against the name
  in the certificate beside it. A person has one credential across organisations: it
  is theirs, not the org's.
- **Expiry.** An expired credential is replaced in place under a new id, so the
  ceremony's cached certificate is never silently swapped: a sign authorization
  naming the old id is refused and the signer links again.

## How the ceremony reaches it: in process

`yivicsc.Provider` is the `provider` the signing service is given in `cmd/api`. A call
for the service's own base URL goes straight to `yivicsc.Service`, the same methods
the HTTP handlers call; any other base URL goes to `signingprovider.Client` as before.
The backend therefore never calls its own public address (which a container often
cannot reach). Only the browser's leg, the authorize URL, is a real request to the
service. The HTTP routes are covered by `service_test.go` driving them with
`signingprovider.Client`, the client the ceremony uses for any provider, so the
service is held to being a CSC provider and not merely a Go API.

- **Its callback is its own.** `SIGNING_REDIRECT_URI` belongs to a remote provider's
  client registration and, unset, is `localhost:8080`. The built-in client is
  registered with `APP_BASE_URL + /api/v1/signing/callback` instead, and
  `Service.UseHostedProvider` makes a ceremony against the service's base URL use that
  (`connection.redirectURI`, kept on the ceremony for the token exchange). Likewise
  `SIGNING_OAUTH_ISSUER_INTERNAL` never applies to it (`tokenIssuer`).
- **Its client.** One, `yivi-business-wallet`, whose secret is derived from the CSC
  key, so replicas agree. `oauth2/authorize` refuses any other `client_id` or
  `redirect_uri` with a 400 and no redirect. Registering a third party's signing
  application is not built.
- **A signer who cancels** comes back with `error=access_denied` and no code;
  `HandleCallback` ends the ceremony there (`ErrAuthorizationDenied`) instead of
  attempting a token exchange. The signer's attempt is marked failed and can be
  retried, as any failed attempt can.

## The authorization page

`oauth2/authorize` redirects to the SPA route `/signing/authorize/{id}`
(`routes/signing-authorize.tsx`), public like `/sign/{token}`. It reads
`GET /yivi-csc/authorizations/{id}`: the wallet link to show as a QR code while
pending, what is being approved (`purpose`, the document's name as `description`,
which `source` to disclose), and once settled the `redirectUrl`. Each read of a
pending authorization asks the verifier whether the signer disclosed. Approved, the
page goes on by itself; denied, it says why and waits for the signer to continue.
`POST …/cancel` denies it. The id is unguessable and is the page's whole
authorization; `oauth2/authorize` is rate limited per client address.

**The wallet's own pages do not go there.** A start against the built-in service
answers `authorizeInApp: true`, and the page that started it (the member's signing
page, the external signee's `/sign/{token}`) asks `authorizeUrl` for JSON:
`oauth2/authorize` answers a caller that accepts `application/json` and not
`text/html` with `{authorizationId, redirectUrl}` instead of the redirect. The page
shows the same step in a dialog (`routes/signing-authorization-dialog.tsx`; the
panel and `lib/use-signing-authorization.ts` are shared with the standalone page),
so the signer scans where they pressed Sign. The decided authorization's
`redirectUrl` is the ceremony's callback, and the dialog asks that for JSON too:
`GET /signing/callback` does its work and answers `{redirectUrl}`, whose query says
how the ceremony ended (`lib/signing-authorization-start.ts`, `ceremonyEnd`), so the
page never reloads. The callback can be answered once, so that request is never
retried; if it fails the browser follows the redirect. Closing the dialog cancels a
pending authorization rather than hiding it: the ceremony holds the request until
its callback ends it. If the JSON start fails the browser is sent to `authorizeUrl`
as before.

**Linking is not a step the signer takes.** Pressing Sign runs whichever of the two
legs are still needed, one after the other, in that dialog
(`lib/use-sign-ceremony.ts`): no certificate yet means link, then sign ("Step 1 of
2", "Step 2 of 2"). A start refused with `no_credential` (the organisation changed
provider) does the same. My credential still shows the certificate and re-links it.
An external signee's view carries `yourTurn` beside `canSign`, since Sign is offered
before they have a certificate. A remote provider still redirects for each leg, and
the signer presses Sign again after linking. A remote provider is never
`authorizeInApp`: its authorization page is its own.

The page shows the document's name because a cross-device QR code can be relayed:
somebody who starts an authorization and gets another person to scan it would be
signing with that person's identity. The Yivi app shows who is asking and for what
data; the page shows what for.

## Preconfigured for every organisation

`internal/csc` gained a provider kind, `yivi`, and a default.

- An organisation with **no row** in `org_csc_settings` reads as
  `{configured, enabled, providerKind: yivi}` and `ResolveConnection` hands the
  ceremony the built-in connection. Nothing is written until an admin saves; no
  migration touches settings.
- An organisation that **had saved** a provider keeps it. An admin switches to the
  built-in service in Settings › Signing (it is listed first); doing so drops the
  remote endpoint, client id and secret.
- **Off means off** for the built-in service: a `yivi` row with `enabled` false
  resolves to no connection, since disabling is how an admin of an org that signed by
  default says it does not sign. (A remote provider's row still resolves while
  disabled, as it always did; only the UI gate reads `enabled` there.)
- The connection test answers the built-in service's own info without a request.
- All of this needs `CSC_ENCRYPTION_KEY`. Without it `buildHostedSigning` logs a
  warning, the service is not mounted, the kind is not offered (409
  `built_in_unavailable` if asked for) and an org with no row has no provider, as
  before. With it, a key that does not open the stored authority fails the boot
  (`Service.Ping`), like every provider probe.

## The signing API (embedding)

`internal/signing/api.go` + `api_handler.go`: the organisation's own software asks
people to sign, with the **same API keys** as the identity proofing API (the
Developers page's tokens; `proofing.Handler.RequireAPIKey`, adapted in
`cmd/api/signing_adapters.go` so `signing` knows nothing of key storage). Two scopes,
`signing:read` and `signing:write`; every key has them (migration
`20261119160300` gave them to existing keys).

| Route | Scope | |
|---|---|---|
| `POST /signing/requests` | write | The wallet's own multipart form (document, `signers`, `mode`, recipient) plus `sendMail` and `returnUrl` |
| `GET /signing/requests`, `GET …/{id}` | read | Status and each signee's own |
| `GET …/{id}/document` | read | The signed PDF; 409 `not_completed` until everyone signed |
| `POST …/{id}/signers/{signerID}/link` | write | A fresh signing link for an external signee |

- **A request made this way has no creator.** `created_by` is NULL, `created_via` is
  `api`, `api_key_id` names the key (a CHECK keeps the two origins apart). `Request`
  keeps a zero `CreatedBy`, so "the creator may read it" never matches by accident.
- **A key reads only requests made over the API**, the split the proofing API keeps
  between external sessions and members' own. The wallet's history shows both, with
  the origin.
- **Links.** The signing link is the external signee's `/sign/{token}` page, where
  they link and sign with Yivi. By default they are mailed it. With `sendMail: false`
  nobody is mailed (`notify_signers`, honoured again when a sequential request moves
  on) and the create answers the link of each external signee who may sign now. A link
  exists nowhere but with the signee (only its hash is stored), so later the caller
  asks for a new one, which replaces the old; it is refused out of turn.
- **`returnUrl`** is stored on the request and comes back on the signee's page as a
  button, with `request_id` and `status` added, once they signed or declined. It is
  https (http for localhost only) and never an automatic redirect: the key holder
  chose it, but the person pressing it should see where they are going.
- **No webhook.** The caller polls. Signing events are already in the audit log and
  the notifications catalogue; a webhook is the obvious follow-up.

## Known limits

- **One instance.** Authorizations, codes and tokens are in memory, as the ceremony's
  own sessions are (the parked PDF pass). Certificates and keys are in the database.
  More than one API replica needs both moved, together.
- **No revocation.** A certificate cannot be revoked and there is no OCSP or CRL; the
  bound is its one-year validity.
- **PAdES level** is what the ceremony makes: B-B, no timestamp.
- **Driving licence** is not a source: it is not a name source the proofing flows
  treat as a travel document, and nobody asked for it.

## Tests

- `internal/yivicsc/service_test.go`: the whole provider over HTTP with a fake
  verifier and an in-memory store: both legs for both families, the mismatch refusal,
  PKCE and code reuse, what `signHash` will and will not sign, expiry, the in-process
  provider.
- `internal/signing/yivi_flow_integration_test.go` (Postgres): the ceremony against
  the real service and schema, a verified PDF, the refusal of another person's wallet,
  and the signing API end to end over the links it hands out.
- `internal/signing/pades_chain_test.go`: a credential of the service's shape (leaf
  under an authority, non-repudiation) through the real signing pass, no database.
- `internal/csc/store_integration_test.go`, `builtin_test.go`: the default and
  switching away and back.
