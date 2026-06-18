# Yivi Business Wallet – Agent Instructions

**Monorepo: Go (gorilla/mux) backend + React 19 / Vite / TypeScript frontend. Run commands from `backend/` or `frontend/`, never the repo root (except `npm install` for hooks).**

Early scaffold — Yivi auth not implemented yet. Backend: `GET /healthz` (DB-backed), `GET /api/v1/ping`, organization CRUD at `GET/POST /api/v1/organizations`. Postgres via GORM; goose migrations run as a dedicated step before the API.

## Structure

```
backend/      Go HTTP API (gorilla/mux). cmd/ = entry points, internal/ = packages
  cmd/api/               API server (serves only, never migrates)
  cmd/migrate/           apply migrations and exit
  internal/config/       env-driven config (DSN)
  internal/database/     GORM/Postgres lifecycle + embedded goose migrations/*.sql
  internal/server/       router assembly + lifecycle (health/ping/logging)
  internal/organization/ example domain slice (model + store + handler)
frontend/     React 19 + Vite + TS + react-router
  src/api/               API clients (read VITE_API_BASE_URL)
  src/routes/            route components, wired via src/router.tsx
regulation/   EU compliance source (FEATURE_LIST.md, COMPLIANCE_MATRIX.md, PDFs)
compose.yaml + compose.override.yaml   Docker Compose (override = live-reload dev)
```

Backend dependency hierarchy (top-down only):
```
handler → store / client
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
```

- `golangci-lint` and `air` are pinned `tool` directives in `backend/go.mod` — invoke via `go tool <name>`, never assume a global install.
- No backend or frontend test runner yet. Don't assume `go test` / `npm test` targets exist.

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

- **Vite proxy hardcodes `http://backend:8080`** (`frontend/vite.config.ts`, the Compose service) — `/healthz` and `/api` only proxy inside Docker. Non-Docker dev: run backend on `:8080`, set `VITE_API_BASE_URL=http://localhost:8080` in `frontend/.env`.
- **Pre-commit runs lint-staged twice**: root `package.json` (`gofmt -w`) + `frontend/.lintstagedrc.json` (prettier + eslint). Install hooks once with `npm install` at the repo root.
- **Config is env-driven**: backend reads `DATABASE_URL` (local default when unset); Compose builds it from root `.env` `POSTGRES_*`. Frontend reads only `VITE_API_BASE_URL`.
- **Migrations are a dedicated step, never on API startup**: the Compose `migrate` service and air `pre_cmd` run `cmd/migrate` first; deploy/k8s runs `go run ./cmd/migrate`. `/healthz` returns 503 when the DB is down; prod image healthchecks via `wget`.
- **Frontend deps auto-install in Docker**: dev entrypoint runs `npm ci` only when `package-lock.json` changed. `node_modules` lives in the `frontend-node-modules` volume — remove it to force a clean reinstall.

## Conventions & Context

Load only when relevant:

| Area | When | File |
|---|---|---|
| Frontend conventions | editing `frontend/` (React / TS / Vite) | `.ai/conventions/FRONTEND.md` |
| Backend conventions | editing `backend/` (Go) | `.ai/conventions/BACKEND.md` |
| Feature / compliance scope | feature planning, regulatory requirements | `regulation/FEATURE_LIST.md` |

If the user makes significant corrections or conventions change, update the relevant file. Keep this file lean.
