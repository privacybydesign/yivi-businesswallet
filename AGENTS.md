# Yivi Business Wallet – Agent Instructions

**Monorepo: Go (stdlib `net/http`) backend + React 19 / Vite / TypeScript frontend. Run commands from `backend/` or `frontend/`, never the repo root (except `npm install` for hooks).**

## Structure

```
backend/      Go HTTP API (stdlib net/http, Go 1.22+ pattern routing)
  cmd/api/               API server (serves only, never migrates)
  cmd/migrate/           apply goose migrations and exit
  cmd/seed/              seed dev data and exit
  internal/config/       env-driven config (DATABASE_URL, LOG_*)
  internal/database/     pgx/v5 Postgres pool lifecycle
  internal/logging/      slog setup + context-aware handler (request ID injection)
  internal/migrate/      embedded goose migrations/*.sql
  internal/organization/ domain slice (model + store + handler)
  internal/respond/      JSON response helpers, HandlerFunc adapter, ApiError
  internal/seed/         dev seed logic
  internal/server/       router assembly, middleware, lifecycle
frontend/     React 19 + Vite + TS + react-router
  src/api/               HTTP transport, resource clients, query hooks
  src/routes/            route components, wired via src/router.tsx
regulation/   EU compliance source (FEATURE_LIST.md, PDFs)
compose.yaml + compose.override.yaml   Docker Compose (override = live-reload dev + seed)
```

Dependency hierarchy (top-down only):
```
handler → respond / store
```

## Commands

Run the full verify sequence (matches CI) before finishing.

**Frontend** (`cd frontend`), in order:
```bash
npm run format      # prettier --check (use format:write to fix)
npm run lint        # eslint . --cache
npm run typecheck   # tsc --build
npm run build       # vite build
```

**Backend** (`cd backend`), in order:
```bash
gofmt -l .                      # must print nothing
go vet ./...
go build ./...
go tool golangci-lint run ./...
go test -race ./...
```

- `golangci-lint`, `air`, and `goose` are pinned `tool` directives in `backend/go.mod` — invoke via `go tool <name>`, never assume a global install.

## Assumptions & Validation

Before implementing a fix based on an assumption:
1. State the assumption explicitly.
2. Find the primary source of truth (DB query, API response, logs).
3. Validate first. Can't check directly → tell the user and ask them to verify.
4. Never chain fixes on unvalidated assumptions. Re-examine before layering.

## General Conventions

Backend + frontend:
- No magic values — named constants.
- Formatter is authoritative — never hand-format (Prettier / `gofmt`).
- No new lint disables without an inline reason; don't silence errors to pass a check.
- Fix the root cause; don't swallow errors silently.
- Keep changes scoped — don't reorder/rewrite unrelated code.
- Match existing patterns in the file/dir you're editing.
- CI-equivalent verify sequence green before finishing.

## Gotchas

- **Vite proxy hardcodes `http://backend:8080`** (`frontend/vite.config.ts`) — health probes and `/api` only proxy inside Docker (dev). For the static production frontend there is no proxy: set `VITE_API_BASE_URL` at build time to point at the centralized backend.
- **Pre-commit runs lint-staged twice**: root `package.json` (`gofmt -w`) + `frontend/.lintstagedrc.json` (prettier + eslint). Install hooks once with `npm install` at the repo root.
- **Config is env-driven**: backend requires `DATABASE_URL` (errors at startup if unset) and reads optional `LOG_LEVEL`, `LOG_FORMAT`, `LOG_SOURCE` (defaults when unset); Compose builds the DSN from root `.env` `POSTGRES_*`. Frontend reads only `VITE_API_BASE_URL`.
- **Migrations run as a dedicated Compose service**, never on API startup and never via air `pre_cmd`. The `migrate` service runs `cmd/migrate` before the API starts; deploy/k8s runs `go run ./cmd/migrate`. Health probes: `/livez` (liveness, always 200) and `/readyz` (readiness, pings DB).
- **Dev Compose also runs a `seed` service** (`cmd/seed`) after migrations — populates dev data.
- **`npm run dev` / `npm run dev:reset`** (repo root): bootstraps the full Docker dev environment. `dev:reset` wipes DB volumes first for a clean slate.
- **Frontend deps auto-install in Docker**: dev entrypoint runs `npm ci` only when `package-lock.json` changed. `node_modules` lives in the `frontend-node-modules` volume — remove it to force a clean reinstall.

## Conventions & Context

Load only when relevant:

| Area | When | File |
|---|---|---|
| Frontend conventions | editing `frontend/` (React / TS / Vite) | `.ai/conventions/FRONTEND.md` |
| Backend conventions | editing `backend/` (Go) | `.ai/conventions/BACKEND.md` |
| Feature / compliance scope | feature planning, regulatory requirements | `regulation/FEATURE_LIST.md` |

If the user makes significant corrections or conventions change, update the relevant file. Keep this file lean.
