# Yivi Business Wallet

SaaS business wallet based on [Yivi](https://yivi.app). Multi-tenant
(organizations), with authentication via Yivi.

## Repository layout

```
backend/    Go (stdlib net/http) HTTP API
frontend/   React 19 + Vite + TypeScript + react-router
compose.yaml + compose.override.yaml   Docker Compose dev/prod orchestration
```

## Prerequisites

- [Go](https://go.dev/dl/) 1.26+
- [Node.js](https://nodejs.org/) 26+
- [Docker](https://docs.docker.com/get-docker/) with Docker Compose

## Quick start (Docker Compose)

From the repo root:

```sh
npm run dev           # start full dev environment (DB + migrate + seed + backend + frontend)
npm run dev:reset     # same, but wipes DB volumes first (clean slate)
```

- Frontend: http://localhost:5173
- Backend: http://localhost:8080

The Vite dev server proxies health probes and `/api` to the backend container, so
the frontend talks to the backend out of the box. (There is no `/irma` proxy —
see below; the phone talks to the IRMA daemon directly.)

## Yivi login (development)

Authentication is a real Yivi (IRMA) disclosure flow. IRMA runs as a **standalone
daemon** (the dev `irma` Compose service); the backend is an IRMA _requestor_ that
starts disclosure sessions against it over HTTP and reads the result. The frontend
renders the QR with `@privacybydesign/yivi-frontend`. To log in you scan the QR
with the [Yivi app](https://yivi.app) and disclose your email attribute.

The daemon exposes two surfaces on one port (`8088` in dev):

- the **requestor API**, which the backend reaches at `http://irma:8088` over the
  Docker network (`IRMA_REQUESTOR_URL`); and
- the **client API**, which the **phone** reaches during a disclosure.

**The catch:** the phone must reach the daemon's client API over the network, and
`localhost` does not work from a device. The session QR embeds an _absolute_ URL
derived from the daemon's `--url` (`IRMA_CLIENT_URL`), so that is the value the
phone uses — point it at a reachable address. Note: the tunnel/LAN URL must front
the **`irma` daemon's** port `8088`, **not** the backend's `8080`.

### Automatic HTTPS tunnel (default — nothing to do)

`npm run dev` handles this for you. The dev stack runs a pinned `cloudflared`
service that opens a Cloudflare **quick tunnel** to the daemon's `8088`; the
dev-setup script reads the tunnel's assigned HTTPS URL from cloudflared's metrics
endpoint, sets `IRMA_CLIENT_URL`, and recreates the `irma` daemon so every QR
points at that tunnel. The phone-facing URL is printed when startup completes.

```sh
npm run dev
# ...
# Phone-facing IRMA URL (scan target): https://something.trycloudflare.com
```

No Cloudflare account is needed and it works on any network (HTTPS, so Yivi app
developer mode can stay **off**). The trade-off: each `npm run dev` depends on
Cloudflare's quick-tunnel service being reachable, and the URL is fresh per run.

### Fallback: LAN IP + Yivi app developer mode

> Skips the auto tunnel: setting `IRMA_CLIENT_URL` yourself makes dev-setup use
> your value instead of starting a quick tunnel.

Without a tunnel, set `IRMA_CLIENT_URL` to your machine's LAN IP and the daemon's
port over plain HTTP, and enable
[developer mode](https://docs.yivi.app/yivi-app#developer-mode) in the Yivi app
(required because the connection is not HTTPS). The phone and dev machine must be
on the same network with client isolation disabled:

```sh
echo 'IRMA_CLIENT_URL=http://192.168.1.2:8088' >> .env   # your LAN IP + daemon port
npm run dev
```

For local testing the dev environment discloses the **demo** email attribute
(`irma-demo.sidn-pbdf.email.email`), so no production credential is needed — issue
yourself the demo credential from the
[attribute index](https://portal.yivi.app/attribute-index/environments/demo).

### Production

Prod does not run this daemon. Point `IRMA_REQUESTOR_URL` at the centralized IRMA
server and set `IRMA_REQUESTOR_TOKEN` if it requires requestor authentication. The
backend runs a one-shot readiness probe at boot (a real `StartSession` with the
configured attribute, immediately cancelled) and **exits non-zero** if the daemon
won't accept it — so a misconfigured `disclose-perms`/scheme fails the deploy, not
the first user. **Cutover checklist item:** confirm with whoever runs the
centralized daemon that starting-and-cancelling one disclosure session per backend
deploy (the boot probe) is acceptable on their side; if not, the probe must be
downgraded to a reachability check and the `disclose-perms` mismatch documented as
surfacing at first login instead.

## Scripts & tooling

### Frontend (`cd frontend`)

| Command                | Description                          |
| ---------------------- | ------------------------------------ |
| `npm run dev`          | Start the Vite dev server            |
| `npm run build`        | Production build                     |
| `npm run preview`      | Preview the production build         |
| `npm run typecheck`    | Type-check with `tsc --build`        |
| `npm run lint`         | Run ESLint                           |
| `npm run lint:fix`     | Run ESLint with `--fix`              |
| `npm run format`       | Check formatting with Prettier       |
| `npm run format:write` | Apply Prettier formatting            |

### Backend (`cd backend`)

| Command                      | Description                            |
| ---------------------------- | -------------------------------------- |
| `go build ./...`             | Build                                  |
| `go vet ./...`               | Vet                                    |
| `gofmt -l .`                 | List unformatted files                 |
| `go tool air -c .air.toml`   | Run with live-reload                   |
| `go tool golangci-lint run`  | Lint (pinned version via `go.mod`)     |
| `go test -race ./...`        | Run tests with race detector           |

Dev tools (`air`, `golangci-lint`, `goose`) are managed as Go
[tool directives](https://go.dev/doc/modules/managing-dependencies#tools) in
`backend/go.mod`. Running `go tool <name>` fetches and builds the pinned version
automatically on first use — no separate install step required.

## Environment variables

The backend requires `DATABASE_URL` and reads optional `LOG_LEVEL`, `LOG_FORMAT`,
and `LOG_SOURCE` (sensible defaults when unset). Compose builds `DATABASE_URL`
from root `.env` `POSTGRES_*` variables; in production it is supplied by the
deployment (e.g. a Kubernetes secret).

Yivi auth adds:

| Variable                 | Default                              | Purpose                                                                         |
| ------------------------ | ------------------------------------ | ------------------------------------------------------------------------------- |
| `IRMA_REQUESTOR_URL`     | `http://irma:8088`                   | Where the backend (requestor) reaches the daemon's requestor API.               |
| `IRMA_REQUESTOR_TOKEN`   | _(empty — daemon runs `--no-auth`)_  | Preshared requestor token, if the daemon requires it (prod).                    |
| `IRMA_CLIENT_URL`        | `http://localhost:8088`              | Phone-facing daemon URL baked into the QR (the daemon's `--url`; tunnel/LAN).    |
| `IRMA_EMAIL_ATTRIBUTE`   | `irma-demo.sidn-pbdf.email.email`    | Disclosed attribute; prod uses `pbdf.sidn-pbdf.email.email`. Must match the daemon's `disclose-perms`. |
| `SESSION_COOKIE_SECURE`  | `false`                              | `true` in prod; also marks the production posture (fail-fast).                  |
| `SESSION_TTL`            | `24h`                                | Session/cookie lifetime (single source for both).                               |
| `SESSION_PRUNE_INTERVAL` | `1h`                                 | How often expired sessions are pruned.                                          |

`IRMA_CLIENT_URL` is consumed by the dev `irma` Compose service (its `--url`), not
by the backend; the backend only dials `IRMA_REQUESTOR_URL`. ("Client" here means
the Yivi **app**, not the backend — the backend is the requestor.)

When `SESSION_COOKIE_SECURE=true`, `IRMA_REQUESTOR_URL` must be set explicitly
(startup fails otherwise) so production never silently talks to the dev default.

The frontend reads only `VITE_API_BASE_URL` — leave empty to use the Vite
dev-server proxy; set it at build time to point the static frontend at a
centralized backend in production.

## Pre-commit hooks

[husky](https://typicode.github.io/husky/) + lint-staged run automatically on
commit: `gofmt` for backend Go files, and Prettier + ESLint for frontend files.
Install hooks once after cloning with `npm install` at the repo root.

## Continuous integration

GitHub Actions (`.github/workflows/ci.yml`) runs format, lint, type-check, build,
and test for both the frontend and backend on every push and pull request.
