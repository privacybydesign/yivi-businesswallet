# PostGuard sidecar

An internal, **backend-only** Node service that performs the one cryptographic
operation the Go API cannot do natively: encrypt one or more files to a set of
e-mail recipients and upload the sealed package via
[PostGuard for Business](https://docs.postguard.eu/), authenticating the sender
with the organization's API key.

It wraps [`@e4a/pg-js`](https://docs.postguard.eu/sdk/js-encryption.html). The
Go backend owns the API key at rest (AES-GCM encrypted in Postgres); this
service is **stateless** — the key arrives with each request and is never
persisted here.

## Why a sidecar

PostGuard ships no Go SDK. Rather than FFI-wrap the Rust core or add a .NET
runtime, we run the maintained TypeScript SDK server-side. Doing it in a
dedicated service (not the browser) keeps the `PG-…` key off the client.

## Security posture

The endpoint is protected by two independent controls:

1. **Network isolation** — in Compose the sidecar is *not* published to the
   host. Only the `backend` service can reach it, by service name, over the
   private network.
2. **Shared secret** — every `/v1/*` request must present
   `Authorization: Bearer <PG_SIDECAR_TOKEN>`, compared in constant time. The
   process refuses to start if the token is unset (fail closed).

Do not expose this service publicly or add a browser-facing CORS policy.

## API

### `GET /healthz`
Unauthenticated liveness probe. `200 {"status":"ok"}`.

### `POST /v1/send`
Authenticated. `multipart/form-data`:

| part | kind | required | notes |
|---|---|---|---|
| `apiKey` | field | yes | the organization's `PG-…` key |
| `recipients` | field | yes | JSON array of recipient e-mail addresses |
| `file` | file(s) | yes | one or more files (encrypted together) |
| `notify` | field | no | `"false"` to skip recipient e-mails (default on) |
| `message` | field | no | notification message |
| `language` | field | no | notification language (default `EN`) |

Success: `200 {"uuid":"<cryptify-id>"}`. Errors are JSON
`{"error":"<code>","message":"..."}` with status `400` (validation), `401`
(auth), `413` (too large), or `502` (PostGuard upstream rejected the request).

## Configuration

See `.env.example`. Only `PG_SIDECAR_TOKEN` is mandatory.

## Development

```bash
npm install
npm run dev        # tsx watch (requires PG_SIDECAR_TOKEN in the env)
npm run build      # tsc -> dist/
npm start          # node dist/server.js
```
