# Dev verifier certificates

Development-only X.509 material for `cmd/devverifier`, the local OpenID4VP relying
party of the `verifier` Compose profile. The leaf's SAN is `devverifier` (the Compose
service name), so its `client_id` is `x509_san_dns:devverifier`; the root is inlined in
`compose.override.yaml` as the backend's `OPENID4VP_VERIFIER_TRUST_CHAIN`, which is the
only place it is trusted. The private key is checked in on purpose: it signs requests to
a wallet that trusts this root nowhere but on a developer's machine.

Regenerate (then update the root in `compose.override.yaml`):

```sh
cd backend && DEVVERIFIER_WRITE_CHAIN_DIR=../dev-setup/devverifier go run ./cmd/devverifier
```
