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
  internal/apidocs/      embedded OpenAPI spec + ReDoc page, served at /api/docs (drift-checked against handlers)
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
npm test            # vitest run (DOM-free unit tests; not yet a CI job)
```

Frontend unit tests use Vitest (node env, no jsdom) and live beside their source as
`*.test.ts`; they cover extracted pure logic, not rendered components. CI does not
run them yet (`.github/workflows/ci.yml` only lints/typechecks/builds the frontend).

`src/lib/audit-event.test.ts` parses the action/target constants out of
`backend/internal/audit/audit.go` and asserts each resolves to a real i18n
string, so a new backend audit action must gain an `auditLog.actions.*` /
`auditLog.targets.*` translation (and a `case` in `audit-event.ts`) or that test
fails — the audit UI would otherwise render the raw dotted key.

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
- **Dev Compose also runs a `seed` service** (`cmd/seed`) after migrations — populates dev data. The flag-less default is the full dev demo seed (faker members, invitations, audit activity) and is dev-only. Two partial modes create no demo data and are safe to run on every staging/production deploy (combinable): `seed -admins` provisions the `PLATFORM_ADMIN_EMAILS` accounts, `seed -org` provisions just the Yivi organisation (idempotent via `ON CONFLICT (slug)`).
- **`npm run dev` (repo root) bootstraps the full Docker dev environment.** Two independent flags, freely combined (the scripts are just presets — pass through with `--`): `--reset` (`npm run dev:reset`) wipes DB volumes first for a clean slate; `--debug` (`npm run dev:debug`) runs `docker compose up` in the **foreground** to stream every service's logs live. Without `--debug` the stack runs **detached** (quiet — only Compose progress + the scan URL print, then it idles until CTRL-C). Combine via `npm run dev -- --reset --debug`. Either way CTRL-C is owned solely by the script's SIGINT handler (`docker compose down`); the foreground `up` is spawned in its own process group so the terminal signal doesn't reach it. `dev-setup` sets `DOCKER_CLI_HINTS=false` + `COMPOSE_MENU=false` to silence Compose's "What's next:" hints and the `/dev/tty` nav-menu warning.
- **Frontend deps auto-install in Docker**: dev entrypoint runs `npm ci` only when `package-lock.json` changed. `node_modules` lives in the `frontend-node-modules` volume — remove it to force a clean reinstall.
- **Auth is OpenID4VP, verified by a hosted EUDI verifier** (this *supersedes* the earlier Yivi/IRMA standalone-daemon design — see git history and `.ai/features/auth-openid4vp.md`). The backend is the **requestor/orchestrator**: `internal/openid4vpverifier` starts a presentation at the verifier (`POST /ui/presentations`), polls the result (`GET /ui/presentations/{transactionId}`), and parses the SD-JWT VC disclosures. It does **not** implement the verifier role or the SD-JWT/key-binding crypto — the hosted verifier does trust-chain verification. Config: `EUDI_VERIFIER_URL` (default: Yivi staging), `EUDI_ISSUER_CHAIN` (trusted-issuer CA PEM). No local daemon, no `/irma` proxy, no phone-facing tunnel — the wallet reaches the hosted verifier directly.
- **Two disclosure scopes, chosen per flow** (`openid4vpverifier.Scope`): `ScopeLogin` discloses **email only** (data minimisation); `ScopeIdentity` discloses **passport OR id-card + email + phone** for flows that must match a real person (invitation accept, wallet registration). DCQL uses `credential_sets` for the passport-OR-idcard choice. `auth.StartSession` → login scope; `auth.StartIdentitySession` → identity scope.
- **The frontend drives the QR + polling** (`ui/identity-disclosure.tsx`): it `POST`s the session-start endpoint, renders the `openid4vp://` deeplink as a QR (via `qrcode`) plus an "open wallet" universal link, and polls `GET /api/v1/auth/session/{id}/status` until `DONE`, then hands the **session id** to the claim/accept/register endpoint. The backend never exposes the verifier's `transaction_id`; it keys on its own opaque session id.
- **Startup readiness is a one-shot probe, not a `/readyz` component**: `cmd/api` calls `openid4vpverifier.Ping` once at boot (creates a login presentation request of the shape it will use). Failure is **fatal** — a misconfigured verifier fails the deploy, not the first login. `/readyz` stays DB-only.
- **Pending/unknown/expired presentations all surface as `ErrPending`**: the hosted verifier returns non-2xx for any of them and doesn't distinguish, so the status endpoint reports `PENDING` (the frontend's own timeout bounds the wait) and `claim` maps it to `session_not_finished` (409). There is no distinct `unknown_session` mapping.
- **Session cookie security posture**: the session cookie is `HttpOnly`, `SameSite=Lax`, host-scoped (`Domain` unset), `Secure` from `SESSION_COOKIE_SECURE`. `SameSite=Lax` is the conscious CSRF defense for the first-party `POST /claim` + `POST /logout` flow — it holds until a state-changing GET or cross-site cookie need appears. The cookie name is a package const (`ybw_session`), not configurable.
- **No phone-facing tunnel or `IRMA_CLIENT_URL`**: the wallet reaches the hosted EUDI verifier directly over the internet, so `npm run dev` no longer runs a Cloudflare quick tunnel or a local `irma`/`cloudflared` Compose service (removed). Point `EUDI_VERIFIER_URL` at a different verifier if needed; `EUDI_ISSUER_CHAIN` must carry the trusted-issuer CA PEM for the verifier to accept presented credentials. Dev uses `pbdf-staging.*` credentials via the Yivi staging verifier — no production credential needed.
- **Holder wallet engine (`internal/eudiholder`) re-adds the `irmago` dependency, but only CGO-free.** The org holds credentials in irmago's EUDI storage backed by Postgres (`ATTESTATION_HOLDER=stub|irmago`, stub default; one Postgres schema per org, `holder_<orghex>`). The backend builds `CGO_ENABLED=0` (static Alpine) and CI runs `go test -race` (forces CGO on) — both must stay sqlcipher-free, so import irmago's `eudi/storage` (CGO-free) and **never** `eudi/storage/sqlcipherstorage` (cgo/libsqlcipher). This needed an upstream split (irmago#622). `backend/go.mod` has a **temporary local `replace`** for irmago pending that merge — drop it and pin the master pseudo-version before merging the holder PR. See `.ai/features/attestations.md` §6.5.
- **Responsive app shell**: `routes/root.tsx` is the authenticated layout (sidebar + `<main>`). Below Tailwind's `lg` breakpoint the sidebar becomes an off-canvas drawer, opened from a hamburger the shared `ui/TopBar` renders via `MobileNavContext`. A new authenticated page must render `<TopBar>` to expose that menu button on mobile — a page without one leaves the drawer unreachable on small screens.
- **Per-org theming splits into two mechanisms by whether a token is mode-safe** (`lib/theme.ts`). The persisted seeds are `primary`, `accent`, `text`, `surface`, `border`, `link`, `success`, `warning`, `error` (all optional hex, `""` = default) plus the logo (`themesettings` / `themeSchema`). (1) **Mode-safe brand fills** (`primary`, `accent`) are self-contained coloured fills with their own readable foreground, so one value is correct in both modes — `resolveThemeTokens` maps them to `--yb-*` overrides applied **inline on the documentElement** (they win over the light *and* dark `:root` defaults). (2) **Mode-aware roles** (`surface`/`text`/`border`/`link`/status) are mode-specific — a tenant's light surface must not be forced into dark mode — so `resolveThemeCss`/`buildThemeCss` derive a light **and** dark value for each (tinting the neutral, or nudging to WCAG-AA against the worst-case surface in that mode) and ship them as one `<style id="ybw-org-theme">` whose dark half sits under a `prefers-color-scheme: dark` media query. That block uses a doubled **`:root:root`** selector so it wins over Vite's CSS by *specificity, not source order* — which is what lets it be pre-painted safely.
- **Per-org theming has a pre-paint cache to avoid a FOUC**: `cacheOrgTheme` mirrors the resolved overrides into `localStorage` under `ybw.theme.<slug>` as `{ inline, css }` (the inline brand-fill map + the mode-aware CSS string); `applyOrgTheme` applies the same two at runtime. The inline script in `frontend/index.html` reads that key by the first path segment and, **before React hydrates**, sets the inline props on the documentElement and injects the `css` as `<style id="ybw-org-theme">`, so a full reload of an org page never flashes the default palette first — including the mode-aware roles (the `:root:root` specificity removes the old source-order race, so links/surfaces are pre-painted too). `resolveThemeTokens`/`resolveThemeCss` are the single source of truth for both paths; if you change the token set or cache key, update the inline reader in `index.html` in the same edit (it only replays the cached values, deriving nothing, so the two cannot drift). `src/lib/theme.test.ts` pins every derived text/status/link pair at WCAG-AA in both modes. The org logo (sidebar + `ui/TopBar`, via `ui/brand.ts` `BrandProvider`) is **not** cached (an `<img>`, member-gated, not a CSS token).
- **Pre-auth screens brand cache-only via `?org=<slug>`**: `/login` and `/register` are slug-free, so `lib/pre-auth-theme.ts` `usePreAuthOrgTheme` reads `?org=<slug>` and replays that org's cached palette (colours only — the logo endpoint is member-gated), falling back to the default Yivi look for a first-time visitor, and clears on unmount so nothing leaks into the authenticated shell.
- **Dark mode is OS-driven, pure CSS.** A single `@media (prefers-color-scheme: dark)` block in `frontend/src/index.css` overrides the neutral/surface/status/elevation `--yb-*` tokens with the design system's dark values (`Yivi Design System.zip` → `ui_kits/business/tokens.css`, `[data-theme="dark"]`); every utility resolves through those tokens so the whole shell flips at once. There is deliberately **no per-user light/dark toggle** — per-user theming is a stated non-goal (issue #64), so dark mode follows the OS preference only. The mode-safe brand seeds (primary/accent) are applied inline on the documentElement and carry into dark mode unchanged; the mode-aware per-org roles (surface/text/border/link/status) each derive a dedicated dark value in the `<style id="ybw-org-theme">` block (see the theming bullet above), so a tenant's palette respects dark mode too. `src/index-css-dark.test.ts` parses the dark block and pins every rendered fg/bg pair at WCAG-AA, so a new or edited dark value can't silently regress contrast. NB: `--yb-muted` runs below AA in **both** themes (a pre-existing house-style choice for incidental captions), so it is not asserted.

## Conventions & Context

Load only when relevant:

| Area | When | File |
|---|---|---|
| Frontend conventions | editing `frontend/` (React / TS / Vite) | `.ai/conventions/FRONTEND.md` |
| Backend conventions | editing `backend/` (Go) | `.ai/conventions/BACKEND.md` |
| Feature / compliance scope | feature planning, regulatory requirements | `regulation/FEATURE_LIST.md` |
| Planning & review | writing a plan or reviewing a branch | `.ai/plans/README.md` |
| Feature docs      | working on an existing capability    | `.ai/features/<name>.md` |

If the user makes significant corrections or conventions change, update the relevant file. Keep this file lean.
