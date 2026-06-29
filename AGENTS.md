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
  internal/audit/        transactional audit-event seam (Recorder write side, Reader read side)
  internal/organization/ domain slice (orgs + memberships + invitations + audit log + org-scoped authz)
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
handler → service → store / client      (when orchestrating 2+ stores/clients)
handler → store / client                (pure CRUD — no service)
```
A service layer is required only when a flow orchestrates multiple stores/clients or carries cross-domain rules (`auth`); pure-CRUD slices skip it (`organization`). See `.ai/conventions/BACKEND.md`.

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

- `go test -race ./...` is DB-free (the integration suite is tag-gated and skips without a database). CI also runs the integration job: `go test -tags=integration -race ./...` against a real Postgres — run it when touching stores, migrations, or the audit seam. See `.ai/conventions/BACKEND.md` for the `TEST_DATABASE_URL` setup.
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
- **Config is env-driven**: backend requires `DATABASE_URL` (errors at startup if unset) and reads optional `LOG_LEVEL`, `LOG_FORMAT`, `LOG_SOURCE`, `PLATFORM_ADMIN_EMAILS` (comma-separated, defaults when unset); Compose builds the DSN from root `.env` `POSTGRES_*`. Frontend reads only `VITE_API_BASE_URL`.
- **Multi-tenancy is unified-identity-per-deployment, swapped by config not code.** One deployment's DB holds one global user per email, linked to orgs via `memberships`; sessions and `/api/v1/auth/*` + `/me*` are central (slug-free). Org-scoped data lives under `/api/v1/orgs/{slug}/...` — `organization.Handler.Authorize` (after `auth.RequireUser`) resolves the slug, lets platform admins through, else requires a membership, and stashes the org + effective role in context (`OrgFromContext`); `RequireOrgAdmin` gates admin-only routes. **Bring-your-own-storage = a self-hosted deployment pointed at the tenant's own `DATABASE_URL`** — their users/orgs are separate because it's a different DB, no per-request routing. Per-tenant *business data* doesn't exist yet; when it does, it gets per-org RLS isolation (see git history / the multi-tenancy plan) behind the unchanged `database.DB` store interface — as does the central-API-to-external-DB variant. Neither is built yet.
- **Migrations run as a dedicated Compose service**, never on API startup and never via air `pre_cmd`. The `migrate` service runs `cmd/migrate` before the API starts; deploy/k8s runs `go run ./cmd/migrate`. Health probes: `/livez` (liveness, always 200) and `/readyz` (readiness, pings DB).
- **Dev Compose also runs a `seed` service** (`cmd/seed`) after migrations — populates dev data.
- **`npm run dev` (repo root) bootstraps the full Docker dev environment.** Two independent flags, freely combined (the scripts are just presets — pass through with `--`): `--reset` (`npm run dev:reset`) wipes DB volumes first for a clean slate; `--debug` (`npm run dev:debug`) runs `docker compose up` in the **foreground** to stream every service's logs live. Without `--debug` the stack runs **detached** (quiet — only Compose progress + the scan URL print, then it idles until CTRL-C). Combine via `npm run dev -- --reset --debug`. Either way CTRL-C is owned solely by the script's SIGINT handler (`docker compose down`); the foreground `up` is spawned in its own process group so the terminal signal doesn't reach it. `dev-setup` sets `DOCKER_CLI_HINTS=false` + `COMPOSE_MENU=false` to silence Compose's "What's next:" hints and the `/dev/tty` nav-menu warning.
- **Frontend deps auto-install in Docker**: dev entrypoint runs `npm ci` only when `package-lock.json` changed. `node_modules` lives in the `frontend-node-modules` volume — remove it to force a clean reinstall.
- **Auth is Yivi (IRMA), via a STANDALONE daemon** (this *supersedes* an earlier embedded-irmaserver design — see git history; the embedded reasoning was correct for its world and was replaced, not extended, on the irmago devs' advice to always run irmago as a service). The backend is the **requestor**: it imports only `irma` + `irma/server` types and talks to the daemon over HTTP via `internal/irmarequestor` (`StartSession`/`Result`/`Status`/`CancelSession`). It does **not** embed `irmaserver` and does **not** mount `/irma/`. Dev runs a local `irma` Compose service (`ghcr.io/privacybydesign/yivi:v1.0.0`, `yivi irma server`); prod points `IRMA_REQUESTOR_URL` at a centralized server we don't deploy.
- **Two ports, two actors**: the daemon's *requestor API* (backend → `irma:8088`, Docker-internal) and its *client API* (phone → tunnel) share port 8088 in dev (single-port; `client-port` unset). The session QR's `sessionPtr.u` is **absolute** (derived from the daemon's `--url` = `IRMA_CLIENT_URL`), so the Yivi app and the in-browser yivi client hit the daemon directly — there is no `/irma` Vite proxy and the backend never serves that traffic. Note the word "client": in IRMA it means the Yivi **app**, never our backend (our backend is the requestor).
- **The logging division is now a network fact, not a mounting decision**: the phone's `/irma` chatter is on a different process entirely. Correlate a stuck session by **requestorToken** in the *daemon's* logs — our request-id never existed there and never will; don't hunt for one.
- **Startup readiness is a one-shot probe, not a `/readyz` component**: `cmd/api` calls `irmarequestor.Ping` once at boot — a real `StartSession` with the configured `emailAttr`, then `CancelSession`. This catches what `depends_on` cannot: not "is the daemon up" but "will it *accept* the request we'll send" (auth reaches it, `disclose-perms` allows the attribute, the scheme resolves) — the exact seam that differs between dev `irma-demo.*` and prod `pbdf.*`. Failure is **fatal**; self-heal is the orchestrator's restart policy, never a recurring readiness check (that would reimplement restart-on-failure one tier too low). `/readyz` stays DB-only. Don't "strengthen" `Ping` into a claim round — it deliberately stops before users/cookies/DB.
- **Unknown/expired token = `SESSION_UNKNOWN`, by error code not HTTP status**: the daemon answers **400** with `{"error":"SESSION_UNKNOWN"}`, *not* 404 (validated against the real daemon — assuming the status would mis-map it to a 500). `irmarequestor` keys on the code → `ErrUnknownSession`; the handler maps that to `unknown_session` (404) → frontend "start over". The restart-before-claim window (daemon's in-memory store lost on restart) surfaces the same way.
- **Requestor auth is a body-level seam, not a header**: `irmarequestor.RequestAuthenticator.Authorize(req) (body, headers, err)` owns *both* the request body and headers, because token auth (plaintext request + bearer header) and JWT auth (the request body becomes a signed JWT) are **not** symmetric — a header-shaped interface fits the first and breaks the second. Dev uses the empty-token impl (daemon runs `--no-auth`); prod drops in a token or JWT impl as config, not a rewrite.
- **Session-state scaling is the daemon's concern now, dev-only in our setup**: the daemon keeps session results in memory (single-replica) and loses them on restart; our backend is stateless w.r.t. IRMA (it polls by token). Multi-replica is the daemon's Redis store (`--store-type=redis`), not our code. We don't worry about it in dev.
- **Session cookie security posture**: the session cookie is `HttpOnly`, `SameSite=Lax`, host-scoped (`Domain` unset), `Secure` from `SESSION_COOKIE_SECURE`. `SameSite=Lax` is the conscious CSRF defense for the first-party `POST /claim` + `POST /logout` flow — it holds until a state-changing GET or cross-site cookie need appears. The cookie name is a package const (`ybw_session`), not configurable.
- **`IRMA_CLIENT_URL` must be reachable from the phone**: it's the daemon's `--url` (phone-facing). `localhost` won't work from a device → use a tunnel (cloudflared/ngrok) fronting the **irma daemon's** port, not the backend. See README. Dev discloses the `irma-demo.*` email attribute so no production credential is needed.
- **Dev auto-tunnels by default; the URL is resolved at runtime, not start time**: `npm run dev` (`dev-setup/index.ts`) runs a pinned `cloudflared` quick-tunnel Compose service to `irma:8088`. Because the daemon bakes `--url` at *start* but a quick tunnel's hostname is known only *after* it connects — and the irma image is `FROM scratch` (no shell for an entrypoint wrapper) — the orchestration lives in `dev-setup`, not Compose: it brings up `irma`+`cloudflared` detached, reads the URL from cloudflared's `/quicktunnel` **metrics** endpoint (deterministic JSON, not log-scraping), sets `IRMA_CLIENT_URL`, then `--force-recreate`s only `irma`. Recreation is safe for the backend boot probe (the *requestor* API is independent of `--url`). Always-on means every `npm run dev` depends on Cloudflare being reachable. Escape hatch: presetting `IRMA_CLIENT_URL` (LAN IP / named tunnel / CI) skips the auto tunnel entirely.

## Conventions & Context

Load only when relevant:

| Area | When | File |
|---|---|---|
| Frontend conventions | editing `frontend/` (React / TS / Vite) | `.ai/conventions/FRONTEND.md` |
| Backend conventions | editing `backend/` (Go) | `.ai/conventions/BACKEND.md` |
| Feature / compliance scope | feature planning, regulatory requirements | `regulation/FEATURE_LIST.md` |

If the user makes significant corrections or conventions change, update the relevant file. Keep this file lean.
