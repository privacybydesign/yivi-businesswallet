# Yivi Business Wallet – Agent Instructions

**Monorepo: Go (gorilla/mux) backend + React 19 / Vite / TypeScript frontend.**
Early scaffold — multi-tenancy (organizations) and Yivi authentication are **not implemented yet**. Today the backend exposes only `GET /healthz` and `GET /api/v1/ping`.

Run commands from `backend/` or `frontend/` as noted — never from the repo root (except `npm install` to set up hooks).

## Structure

```
backend/      Go HTTP API (gorilla/mux). Single main package today (main.go)
frontend/     React 19 + Vite + TS + react-router
  src/api/        API clients (read VITE_API_BASE_URL)
  src/routes/     Route components, wired via src/router.tsx
regulation/   EU compliance source (FEATURE_LIST.md, COMPLIANCE_MATRIX.md, PDFs)
compose.yaml + compose.override.yaml   Docker Compose dev/prod (override = live-reload)
```

## Commands

Run the full verify sequence (matches CI) before finishing.

**Frontend** (`cd frontend`), in this order:
```bash
npm run format      # prettier --check  (CHECK ONLY — use format:write to fix)
npm run lint        # eslint . --cache
npm run typecheck   # tsc --build
npm run build       # vite build
```

**Backend** (`cd backend`), in this order:
```bash
gofmt -l .                      # must print nothing (CI fails on unformatted)
go vet ./...
go build ./...
go tool golangci-lint run ./...
```

- **`golangci-lint` and `air` are NOT global installs** — they are pinned `tool` directives in `backend/go.mod`. Always invoke via `go tool golangci-lint run` / `go tool air -c .air.toml`. First run fetches/builds the pinned version automatically.
- No backend tests exist yet; no frontend test runner is configured yet. Don't assume `npm test` or `go test` targets exist.

## Gotchas

- **Vite proxy hardcodes `http://backend:8080`** (`frontend/vite.config.ts`) — the Docker Compose service name. Proxying `/healthz` and `/api` only resolves inside Docker. For non-Docker frontend dev, run the backend on `:8080` and set `VITE_API_BASE_URL=http://localhost:8080` in `frontend/.env`.
- **Pre-commit runs lint-staged twice**: root `package.json` (`gofmt -w` on Go files) and `frontend/.lintstagedrc.json` (prettier + eslint). Install hooks once with `npm install` at the repo root.
- Backend currently reads **no environment variables**; frontend reads only `VITE_API_BASE_URL`.

## General Conventions

Apply to both backend and frontend:

- No magic values — extract named constants.
- The formatter is authoritative — never hand-format against it (Prettier for frontend, `gofmt` for backend).
- No new lint disables without an inline reason; don't silence errors to make a check pass.
- Prefer fixing the root cause over swallowing errors. Don't swallow errors silently.
- Keep changes scoped — don't reorder or rewrite unrelated code.
- Match the existing patterns in the file/directory you're editing.
- Get the CI-equivalent verify sequence green before finishing.

## Conventions & Context

Load only when relevant:

| Area | When | File |
|---|---|---|
| Frontend conventions | editing `frontend/` (React / TS / Vite) | `.ai/conventions/FRONTEND.md` |
| Backend conventions | editing `backend/` (Go) | `.ai/conventions/BACKEND.md` |
| Feature / compliance scope | feature planning, regulatory requirements | `regulation/FEATURE_LIST.md` |

Keep this file lean — update it on the go as conventions emerge.
