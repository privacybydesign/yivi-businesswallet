# API documentation

The backend serves its own OpenAPI spec and a browsable [ReDoc](https://github.com/Redocly/redoc) page:

- `GET /api/docs` — ReDoc UI (`redoc.html`)
- `GET /api/docs/openapi.yaml` — the raw OpenAPI 3 spec (`openapi.yaml`)

Both are read-only, unauthenticated, and mounted on the root mux outside the
`/api/v1` prefix so they are never mistaken for API resources. The spec and the
HTML shell are `//go:embed`-ed into the API binary.

## Keeping the spec in sync

The spec is hand-authored (generated from a route table) rather than derived
from handler annotations. Two tests in this package fail CI when it drifts:

- `TestSpecCoversAllRoutes` — parses every `mux.Handle("METHOD /path", …)` route
  out of the handler source and asserts it is documented, and that the spec
  documents no `/api/v1` route that no longer exists.
- `TestSpecIsValidOpenAPI` — validates `openapi.yaml` against the OpenAPI 3
  schema.

So adding or removing an endpoint without updating `openapi.yaml` breaks the
build. When that happens, edit `openapi.yaml` to match: add or remove the path
item, keeping the existing shape (tags, `security`, path parameters, and the
shared error responses under `components/responses`).
