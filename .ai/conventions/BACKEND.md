# Backend Conventions – Go (stdlib net/http)

Load when editing `backend/`. General rules (magic values, formatter, scoped changes, no silent error swallowing) live in `AGENTS.md`.

Layout: entry points in `cmd/api` + `cmd/migrate` + `cmd/seed`, packages under `internal/`. Two domain templates: `internal/organization` is the no-orchestration example (handler → store), `internal/auth` is the with-service example (handler → service → stores/client) — anchor new domains on whichever fits (see Layering). `internal/respond` provides JSON response helpers, `HandlerFunc` adapter, and `ApiError`. `internal/seed` populates dev data (runs via the Compose `seed` service) — it seeds through the real stores (so seeded writes are audited like production) and uses `gofakeit` with a fixed seed for deterministic randomized data; CSPRNG is reserved for secrets, never demo data.

## Toolchain

- `gofmt` mandatory — CI fails on unformatted files.
- `go vet ./...` and `go tool golangci-lint run ./...` pass clean.
- Dev tools (`air`, `golangci-lint`) are pinned `tool` directives in `go.mod` — `go tool <name>`, never a global install.
- `sloglint` (bundled with `golangci-lint`) enforces consistent `slog` usage — use typed `slog.Attr` helpers (`slog.String`, `slog.Int`, `slog.Any`, etc.), not bare key-value pairs.

## Routing & HTTP

- stdlib `net/http.ServeMux` (Go 1.22+ pattern routing with `{param}` path values). No framework.
- Router assembled in `internal/server`; each domain exposes `Register(*http.ServeMux)` via the `Registerer` interface.
- Version under `/api/v1/` prefix via sub-mux + `http.StripPrefix`; health probes `/livez` and `/readyz` sit outside the prefix.
- Cross-cutting concerns are middleware via plain `func(http.Handler) http.Handler` wrappers, composed in `internal/server/middleware.go`. Order: `requestID` (outermost) → `recoverer` → `requestLogger`.
- Handlers: set `Content-Type` explicitly; check + log the error from `w.Write`.
- JSON field names are `camelCase` (`json:"createdAt"`) — explicit tags, not Go's exported-field default.

## Server Lifecycle

- `cmd/api/main.go` owns it: builds the `*http.Server`, serves, and shuts down gracefully (traps `SIGINT`/`SIGTERM`, `Shutdown(ctx)` with timeout).
- Startup, shutdown start, and shutdown completion are all logged.
- **External-provider seam (uniform shape):** every external trust/service dependency is a thin typed client behind a consumer-defined interface, chosen by config with an in-process **stub** as the dev/CI default and a **fatal boot `Ping`** at startup (a misconfigured provider fails the deploy, not the first user action). `main.go` has a `new<Provider>` switch per dependency. Instances: `openid4vpverifier` (verify), `openid4vciissuer` (issue — `VeramoIssuer`/`StubIssuer`), `eudiholder` (holder wallet engine — `Engine` (irmago EUDI, Postgres, one schema per org) / `StubHolder`), `qerdsprovider` (QERDS), `registryprovider` (KVK). `/readyz` stays DB-only — provider health is a boot gate, not a readiness component.

## Structured Logging & Request Correlation

- `log/slog` via the stdlib. Configured centrally in `internal/logging` — call `logging.Setup(level, format, source)` once at startup.
- **Format/level/source configurable via env:** `LOG_LEVEL` (debug/info/warn/error, default info), `LOG_FORMAT` (text/json, default text), `LOG_SOURCE` (true/false, default true).
- Output goes to **stdout** (12-factor); never stderr for application logs.
- **Context-aware handler** (`internal/logging/handler.go`): wraps the base handler to read contextual attributes (e.g. request ID) from `context.Context` and inject them into every log record. This is the Go-team-endorsed idiom — attributes in context, not the logger instance.
- **Request IDs:** middleware generates/propagates `X-Request-Id`, stores it via `logging.ContextWithRequestID`. Use `slog.InfoContext(ctx, ...)` / `slog.ErrorContext(ctx, ...)` in handlers/stores so every log line for a request is correlatable.
- **Typed attrs only:** use `slog.String("key", val)`, `slog.Int("key", val)`, `slog.Any("key", val)` — never bare `"key", val` alternating pairs. `sloglint` enforces this.
- **Invariant:** contextual request attributes are added at top level. Do not call `WithGroup` before `InfoContext`/`ErrorContext` in request-handling code, or `request_id` will nest inside the group.

## Layering

Top-down only:
```
handler → service → store / client      (flow orchestrates 2+ stores/clients or carries cross-domain rules)
handler → store / client                (pure CRUD — no service)
```

- A service is required only when a flow orchestrates 2+ stores/clients or carries cross-domain business rules; pure-CRUD slices skip it. `auth.Service` owns the disclosure→user→session login flow and its idempotency key; the auth handler stays HTTP-only (parse, cookies, status mapping). `organization` has no service — the handler calls the store directly.
- The service is additive, not a replacement: per-entity logic stays in the store (validation, slug derivation, sentinel-error translation). The service only coordinates collaborators and holds the cross-domain rules; the handler parses/validates the request, calls a service (or store, for CRUD), and uses `internal/respond` to write JSON or error responses.
- A facade above services is admitted when coordination across services is needed later — not built yet.
- Inject dependencies; no package-level globals for state.
- Accept interfaces, return structs: constructors return concrete (`func NewStore(...) *Store`); define interfaces in the consumer, listing only the methods it uses.
- Translate storage errors to package sentinels (`organization.ErrNotFound`, `ErrSlugTaken`) so handlers branch without importing the database driver. Use `%w` wrapping to preserve `errors.Is` matching.
- Accepted cross-domain seam: `session.Lookup` runs one `sessions JOIN users` query and returns `user.User`. Keep this single-query seam; do not split it behind the service.
- **Tenant access seam:** org-scoped routes (`/orgs/{slug}/...`) compose `auth.RequireUser` → `organization.Handler.Authorize` (resolves the org, platform-admin-or-member check, stashes org + role in context) → optional `organization.RequireOrgAdmin`. New org-scoped slices read the org via `organization.OrgFromContext` and register their routes the same way; identity (users/sessions) stays central and slug-free.

## Auditing

Mutations that change organization-visible state are recorded as audit events through the `internal/audit` seam.

- **Write inside the same transaction as the mutation.** Wrap the change in `database.InTx(ctx, s.db, func(q database.Querier) error { ... })` and call `s.audit.Record(ctx, q, action, target, metadata)` on the same `q` — the event and the change commit or roll back together. Stores hold an `audit.Recorder` (inject `audit.NewDBRecorder()`; tests use `audit.NopRecorder`).
- **Action + target are typed constants** in `audit.go` (`audit.MembershipInvited`, `audit.TargetMembership`, …). Add a constant rather than a string literal; keep distinct actions distinct (`membership.invite_revoked` ≠ `membership.revoked`).
- **Metadata is a uniform `{before, after}` envelope** built with `audit.Created(after)` / `audit.Updated(before, after)` / `audit.Deleted(before)`. The frontend renders any event generically from this shape (a create/delete shows the snapshot, an update diffs changed fields) — don't invent per-action shapes.
- **Store readable values, not ids.** Resolve a department to its name, a role to its label, etc. in the same query (e.g. a `RETURNING (SELECT name …)` subquery) so the log is meaningful without joins. Use `camelCase` keys (they reach the frontend as JSON).
- **Don't audit security internals** (token hashes, session material). The actor comes from `audit.ContextWithActor` and the request id from the logging context — never put them in metadata.
- The read side (`audit.Reader`, `audit.NewReader(pool)`) backs the audit-log API; it projects events for display (`EventActor`), separate from the write-side `Actor`.

## Migrations

- **Author via the goose CLI**, `go tool goose -dir internal/migrate/migrations create <name> sql`. Files are embedded and applied by `cmd/migrate` (and the Compose `migrate` service) — never on API startup.
- **One logical table per migration.** Don't pile multiple `CREATE TABLE`s into one file; don't split a single table's columns across files.
- **Pre-production: edit migrations in place.** We are not in prod, so amend the existing migration for a table rather than stacking `ALTER` migrations on top — keep the schema history clean. (This changes once there is data to preserve.)
- PKs are `gen_random_uuid()` (UUIDv4, core built-in since Postgres 13). The target DB is Postgres 16, so we do **not** use `uuidv7()` (Postgres 18+ only). Always include `down` so `goose down` works.

## Errors & Logging

- Wrap with context: `fmt.Errorf("doing X: %w", err)`. Don't discard errors.
- Wrap once, at the layer that adds context: the store wraps storage errors; handlers map them to a status code, never re-wrap.
- Return errors up the stack; only the handler decides the HTTP response.
- Always log 5xx errors in the handler using `slog.ErrorContext(r.Context(), ...)` before writing the error response. Never silently swallow internal errors.
- Use `slog.ErrorContext` / `slog.InfoContext` (context-aware variants) so request_id is included automatically.

## Config

- Read config/secrets from the environment via `internal/config`: `DATABASE_URL` is required (startup errors if unset); `LOG_LEVEL`, `LOG_FORMAT`, `LOG_SOURCE` are optional with defaults. Never hardcode ports, hosts, regions.
- Group packages by domain as the codebase grows.

## Testing

- `testing/slogtest.Run` validates custom `slog.Handler` implementations against the stdlib conformance suite.
- `net/http/httptest` for middleware and handler tests.
- `go test ./...` from `backend/`.
- **Integration tests** are tagged `//go:build integration` and need a real Postgres via `TEST_DATABASE_URL`; they `t.Skip` when it is unset, so the default `go test ./...` stays DB-free and green. They run against the real schema (`migrate.Up`) and the assembled router (`server.New`), faking only the IRMA daemon (the `auth.irmaRequestor` seam). `internal/testdb.Fresh(t)` provisions an isolated, migrated database per test (dropped on cleanup). Store-level tests live beside each store; full HTTP-flow tests live in `internal/integration`. Run them with `go test -tags=integration -race ./...`; for a local DB: `docker run --rm -e POSTGRES_PASSWORD=postgres -p 5432:5432 postgres:16-alpine` then `TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable`. CI runs this in the `backend-integration-test` job with a Postgres service container. Note these tagged files are not linted by the default `golangci-lint run` (which doesn't pass the tag).
