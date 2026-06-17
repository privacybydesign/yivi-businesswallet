# Backend Conventions – Go (gorilla/mux)

Load when editing `backend/`. General rules (magic values, formatter authority, scoped changes, no silent error swallowing) live in the root `AGENTS.md` — not repeated here.

The backend is an early scaffold (`main.go` only). The rules below encode the intended direction; follow them as the package structure grows, and anchor on the patterns already present in `main.go`.

## Toolchain

- `gofmt` is mandatory — CI fails on any unformatted file. Never hand-format against it.
- `go vet ./...` and `go tool golangci-lint run ./...` must pass clean.
- Dev tools (`air`, `golangci-lint`) are pinned `tool` directives in `go.mod` — invoke via `go tool <name>`, never assume a global install.

## Routing & HTTP

- gorilla/mux, no web framework. Register routes in one place (today `main.go`).
- Version the API under a subrouter: `router.PathPrefix("/api/v1").Subrouter()`. Keep the `/healthz` health check outside the versioned prefix.
- Restrict each route to its method (`.Methods(http.MethodGet)`).
- Cross-cutting concerns are mux middleware wired via `router.Use` (e.g. the existing `logging` middleware).
- Handlers: set `Content-Type` explicitly, and check + log the error from `w.Write` (existing pattern). One handler per endpoint as the surface grows.

## Server Lifecycle

- Preserve graceful shutdown: trap `SIGINT`/`SIGTERM` and call `server.Shutdown(ctx)` with a timeout (already wired in `main.go`).
- Keep the explicit `http.Server` timeouts (`ReadTimeout`, `WriteTimeout`, `IdleTimeout`) set — don't drop them.

## Layering (intended)

Top-down dependencies only:

```
handler  →  service  →  repository / client
```

- Inject dependencies; no package-level globals for state.
- Keep business logic out of handlers — handlers parse/validate input, call a service, write the response.

## Errors & Logging

- Wrap errors with context: `fmt.Errorf("doing X: %w", err)`. Don't discard errors.
- Return errors up the stack; only the top layer (handler) decides the HTTP response.
- Use structured logging with context as the logging approach is chosen; until then follow the `log.Printf` pattern in `main.go` and always include the failing operation.

## Config

- Read config and secrets from the environment (none today). Never hardcode ports, hosts, or regions — extract named constants.
- Group packages by domain as the codebase grows; avoid a single sprawling `main` package long-term.
