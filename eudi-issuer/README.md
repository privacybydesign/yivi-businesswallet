# EUDI issuer

The EU reference issuer
([`eudi-srv-web-issuing-eudiw-py`](https://github.com/eu-digital-identity-wallet/eudi-srv-web-issuing-eudiw-py))
that issues the WE BUILD rulebook credentials (EUCC, EU-PoA, LEI, VAT-ID, UBO). The
backend sends those offers to it (`ATTESTATION_RULEBOOK_ISSUER_URL`). Delivery publishes
it as `ghcr.io/privacybydesign/yivi-businesswallet/eudi-issuer`; the ops repo deploys it
with its configuration, keys and credential definitions (`eudi-issuer.tf`).

## The patch

The backend keeps the Token Status List of these credentials (`internal/issuerstatus`).
The issuer reserves an entry there for each credential it signs (its
`revocation.take_url`), but upstream sends only the doctype, country and expiry, so the
backend cannot tell which offer an entry is for. `issuer-state-in-take.patch` adds the
offer's `issuer_state` to that call.

## Moving to another upstream tag

Change the tag and digest of the first `FROM` in `Dockerfile`. The build fails if the
patch no longer applies; fix it against the new tag's `app/formatter_func.py` and
`app/dynamic_func.py`. The backend's client depends on the offer format and the
unauthenticated `/credentialOfferReq2`, so test on staging before a release.
