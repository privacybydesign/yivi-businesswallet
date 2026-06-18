# Backend Conventions – Go (gorilla/mux)

Load when editing `backend/`. General rules (magic values, formatter, scoped changes, no silent error swallowing) live in `AGENTS.md`.

Layout: entry points in `cmd/api` + `cmd/migrate`, packages under `internal/`. `internal/organization` is the canonical domain example — anchor new domains on it.

## Toolchain

- `gofmt` mandatory — CI fails on unformatted files.
- `go vet ./...` and `go tool golangci-lint run ./...` pass clean.
- Dev tools (`air`, `golangci-lint`) are pinned `tool` directives in `go.mod` — `go tool <name>`, never a global install.

## Routing & HTTP

- gorilla/mux, no framework. Router assembled in `internal/server`; each domain exposes `RegisterRoutes(*mux.Router)`.
- Version under `router.PathPrefix("/api/v1").Subrouter()`; keep `/healthz` outside the prefix.
- Restrict each route to its method: `.Methods(http.MethodGet)`.
- Cross-cutting concerns are middleware via `router.Use` (e.g. `logging`).
- Handlers: set `Content-Type` explicitly; check + log the error from `w.Write`.
- JSON field names are `snake_case` (`json:"created_at"`) — explicit tags, not Go's exported-field default.

## Server Lifecycle

- `internal/server` owns it: `New(db)` builds the `*http.Server`, `Run(srv)` serves + shuts down gracefully (traps `SIGINT`/`SIGTERM`, `Shutdown(ctx)` with timeout).
- Keep the explicit `http.Server` timeouts (`ReadTimeout`, `WriteTimeout`, `IdleTimeout`).

## Layering

Top-down only:
```
handler → store / client
```

- Inject dependencies; no package-level globals for state.
- No service layer: the store owns persistence + small domain logic (validation, slug derivation). Handlers parse/validate the request, call a store method, map result/error to a response.
- Accept interfaces, return structs: constructors return concrete (`func NewStore(...) *Store`); define interfaces in the consumer, listing only the methods it uses.
- Translate storage errors to package sentinels (`organization.ErrNotFound`, `ErrSlugTaken`) so handlers branch without importing GORM.

## Errors & Logging

- Wrap with context: `fmt.Errorf("doing X: %w", err)`. Don't discard errors.
- Wrap once, at the layer that adds context: the store wraps storage errors; handlers map them to a status code, never re-wrap.
- Return errors up the stack; only the handler decides the HTTP response.
- `log.Printf` until structured logging is chosen; always include the failing operation.

## Config

- Read config/secrets from the environment via `internal/config` (single `DATABASE_URL`, local-dev default). Never hardcode ports, hosts, regions.
- Group packages by domain as the codebase grows.
