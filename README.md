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
the frontend talks to the backend out of the box.

## Local development (without Docker)

### Backend

```sh
cd backend
go run ./cmd/api                # run the server
go tool air -c .air.toml        # run with live-reload
```

### Frontend

```sh
cd frontend
npm ci
npm run dev
```

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

The backend reads `DATABASE_URL`, `LOG_LEVEL`, `LOG_FORMAT`, and `LOG_SOURCE`
(sensible local-dev defaults when unset). Compose builds the DSN from root `.env`
`POSTGRES_*` variables.

The frontend reads only `VITE_API_BASE_URL` — leave empty to use the Vite
dev-server proxy.

## Pre-commit hooks

[husky](https://typicode.github.io/husky/) + lint-staged run automatically on
commit: `gofmt` for backend Go files, and Prettier + ESLint for frontend files.
Install hooks once after cloning with `npm install` at the repo root.

## Continuous integration

GitHub Actions (`.github/workflows/ci.yml`) runs format, lint, type-check, build,
and test for both the frontend and backend on every push and pull request.
