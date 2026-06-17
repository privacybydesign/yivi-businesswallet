# Frontend Conventions – React 19 / Vite / TypeScript

Load when editing `frontend/`. General rules (magic values, formatter authority, scoped changes, no silent error swallowing) live in the root `AGENTS.md` — not repeated here.

## TypeScript

- `strict: true`, `noEmit` — type-checking via `tsc --build`, Vite handles transpilation.
- `verbatimModuleSyntax: true` — type-only imports **must** use `import type { Foo }`. Mixing value and type imports without `type` is an error.
- `isolatedModules` + bundler module resolution — no `const enum`, no namespace tricks.
- No `any`. Prefer `unknown` + a narrowing guard. Never `as any` to silence the compiler.
- Explicit return types on exported functions and on component prop types.

## Imports

- External packages first, then local imports. Don't reorder existing imports unnecessarily.
- Keep `import type` separate from value imports per `verbatimModuleSyntax`.

## ESLint

- Flat config (`eslint.config.ts`) uses `recommendedTypeChecked` (type-aware), `react-hooks` (rules-of-hooks), and `react-refresh`.
- Runs with `--cache`; `dist` is globally ignored.
- No new disables without an inline `// eslint-disable-next-line <rule> // reason`.

## Prettier

- Default config (`.prettierrc` is `{}`) — double quotes, semicolons, 2-space indent, trailing commas as Prettier decides.
- `npm run format` is check-only; use `npm run format:write` to apply. Never hand-format.

## Structure & Patterns

- Routes live in `src/routes/` and are wired through `src/router.tsx` (react-router). Add new pages there.
- All backend calls go through `src/api/` — never `fetch` directly from components.
- API base URL comes from `import.meta.env.VITE_API_BASE_URL ?? ""` (empty string = use the Vite dev-server proxy). Don't hardcode hosts.
- File names: kebab-case.
- Organize by feature as the app grows; co-locate route + its API concerns where it reads cleanly.

### API layer (`src/api/`)

- **Transport** lives in `src/api/http.ts`: the `request()` helper handles base URL, JSON encode/decode, timeout/abort, auth headers, and retries. Don't call `fetch` elsewhere.
- **Resource clients** are one file per resource (e.g. `src/api/health.ts`), exporting plain async functions that call `request()`.
- **Query hooks** for TanStack Query live alongside as `<resource>.queries.ts` (e.g. `src/api/health.queries.ts`), exporting a `<resource>QueryKey` and `use<Resource>Query()`.
- **Validate, don't cast**: define a zod schema per response and pass it to `request({ schema })`; derive the type via `z.infer`. Never cast `res.json()`.
- **Errors**: `request()` throws `ApiError` (non-2xx, carries `status`/`body`/`url`) or `ApiValidationError` (schema mismatch). Don't swallow these in components — surface them via the query's `isError`/`error`.
- **Retries**: idempotent GETs only (network errors + 5xx, capped backoff). Retry lives in the transport, so the `QueryClient` sets `retry: false` to avoid compounding.
- **Auth**: `getAuthHeaders()` in `http.ts` is a no-op stub today; Yivi auth (and central 401 handling) plugs in there without touching callers.

### Data fetching (TanStack Query)

- A single `QueryClient` is provided in `src/main.tsx` via `QueryClientProvider`.
- Components consume data through query hooks (`use<Resource>Query`), not manual `useEffect` + `useState`.
- Use stable, exported query keys so cache entries are shared and invalidatable.
