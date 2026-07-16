# Yivi Business Wallet

SaaS business wallet based on [Yivi](https://yivi.app). Multi-tenant
(organizations), with authentication via Yivi. Organizations manage members
through an invitation lifecycle, and every mutation is recorded in a per-org
audit log.

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

On first run `npm run dev` creates a root `.env` from `.env.example` if one does
not exist. Compose **requires** `POSTGRES_PASSWORD` (there is no weak default), so
without this file it would abort with `required variable POSTGRES_PASSWORD is
missing a value`. The seeded `CHANGE_ME` placeholder is fine for a throwaway local
dev database; set a strong value before exposing the stack anywhere. If you run
`docker compose` directly (without `npm run dev`), create the file yourself first:

```sh
cp .env.example .env      # then edit POSTGRES_PASSWORD to a strong, unique value
```

The Vite dev server proxies health probes and `/api` to the backend container, so
the frontend talks to the backend out of the box.

## Login (development)

Authentication is an **OpenID4VP** disclosure verified by a hosted **EUDI verifier**.
The backend is a _requestor_: it starts a presentation at the verifier
(`internal/openid4vpverifier`) and polls the result; the frontend renders the wallet
QR / deeplink and polls the backend by session id. Login discloses only your **email**;
the invitation-accept and business-wallet **registration** flows additionally disclose a
verified **identity** (passport or id-card).

By default the backend uses the Yivi **staging** EUDI verifier
(`https://verifierapi.openid4vc.staging.yivi.app`), reachable over the public internet, so
there is **no local daemon and no tunnel** to run. Scan the QR with a wallet holding the
relevant `pbdf-staging.*` credentials (email, plus passport/id-card for the identity
flows).

Override the verifier with `EUDI_VERIFIER_URL`, and pin the trusted issuer CA with
`EUDI_ISSUER_CHAIN` (PEM) so the verifier accepts presented credentials.

### Production

Point `EUDI_VERIFIER_URL` at the production EUDI verifier and set `EUDI_ISSUER_CHAIN`.
The backend runs a one-shot readiness probe at boot (it creates a presentation request of
the shape it will use) and **exits non-zero** if the verifier won't accept it — so a
misconfigured verifier fails the deploy, not the first user.

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
| `go test -race ./...`        | Run unit tests (DB-free)               |
| `go test -tags=integration -race ./...` | Run integration tests (needs `TEST_DATABASE_URL`) |

Dev tools (`air`, `golangci-lint`, `goose`) are managed as Go
[tool directives](https://go.dev/doc/modules/managing-dependencies#tools) in
`backend/go.mod`. Running `go tool <name>` fetches and builds the pinned version
automatically on first use — no separate install step required.

Integration tests are tag-gated and skip unless `TEST_DATABASE_URL` points at a
real Postgres. For a throwaway one:

```sh
docker run --rm -e POSTGRES_PASSWORD=postgres -p 5432:5432 postgres:18-alpine
TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable \
  go test -tags=integration -race ./...
```

## Environment variables

The backend requires `DATABASE_URL` and reads optional `LOG_LEVEL`, `LOG_FORMAT`,
and `LOG_SOURCE` (sensible defaults when unset). Compose builds `DATABASE_URL`
from root `.env` `POSTGRES_*` variables; in production it is supplied by the
deployment (e.g. a Kubernetes secret). `POSTGRES_PASSWORD` is **required** — Compose
refuses to start without it (no weak default). `npm run dev` seeds a root `.env`
from `.env.example` on first run; for direct `docker compose` use, run
`cp .env.example .env` and set a strong, unique `POSTGRES_PASSWORD` first.

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
