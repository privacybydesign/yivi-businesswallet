# Frontend Conventions – React 19 / Vite / TypeScript

Load when editing `frontend/`. General rules (magic values, formatter, scoped changes, no silent error swallowing) live in `AGENTS.md`.

## TypeScript

- `strict: true`, `noEmit` — type-check via `tsc --build`, Vite transpiles.
- `verbatimModuleSyntax: true` — type-only imports **must** use `import type { Foo }`.
- `isolatedModules` + bundler resolution — no `const enum`, no namespace tricks.
- No `any`. Prefer `unknown` + narrowing guard. Never `as any` to silence the compiler.
- Explicit return types on exported functions and component prop types.

## Imports

- External packages first, then local. Don't reorder existing imports unnecessarily.
- Keep `import type` separate from value imports (`verbatimModuleSyntax`).

## ESLint / Prettier

- Flat config (`eslint.config.ts`): `recommendedTypeChecked`, `react-hooks`, `react-refresh`. Runs `--cache`; `dist` ignored.
- No new disables without inline `// eslint-disable-next-line <rule> // reason`.
- Prettier default (`.prettierrc` = `{}`): double quotes, semicolons, 2-space, trailing commas. `npm run format` is check-only; `format:write` applies.

## Structure & Patterns

- Routes in `src/routes/`, wired through `src/router.tsx` (react-router).
- All backend calls go through `src/api/` — never `fetch` directly from components.
- API base URL: `import.meta.env.VITE_API_BASE_URL ?? ""` (empty = Vite proxy). Don't hardcode hosts.
- File names: kebab-case.
- Organize by feature as the app grows; co-locate route + its API concerns where it reads cleanly.

### API layer (`src/api/`)

- **Transport** in `src/api/http.ts`: `request()` handles base URL, JSON, timeout/abort, auth headers, retries. Don't call `fetch` elsewhere.
- **Resource clients**: one file per resource (`src/api/health.ts`), exporting plain async functions calling `request()`.
- **Query hooks**: co-located `<resource>.queries.ts`, exporting `<resource>QueryKey` + `use<Resource>Query()`.
- **Validate, don't cast**: zod schema per response, passed to `request({ schema })`; type via `z.infer`. Never cast `res.json()`.
- **Errors**: `request()` throws `ApiError` (non-2xx; carries `status`/`body`/`url`) or `ApiValidationError` (schema mismatch). Surface via the query's `isError`/`error`, don't swallow.
- **Retries**: idempotent GETs only (network + 5xx, capped backoff), in the transport — so `QueryClient` sets `retry: false`.
- **Auth**: `getAuthHeaders()` in `http.ts` is a no-op stub; Yivi auth + central 401 handling plug in there without touching callers.

### Data fetching (TanStack Query)

- Single `QueryClient` provided in `src/main.tsx`.
- Consume data through query hooks, not manual `useEffect` + `useState`.
- Stable, exported query keys so cache entries are shared and invalidatable.

### i18n (react-i18next)

- **No user-facing string literals in components** — every label/placeholder/`aria-label`/message goes through `t()`. Brand tokens (`"Yivi"`, the `"Business"` wordmark in `logo.tsx`) are the only exception.
- `src/i18n/locales/en.ts` is the typed source of truth (`as const`); `i18next.d.ts` augments `i18next` so `t("...")` **keys** are compile-time checked. Caveat: interpolation **variable presence** (`{{email}}`) is *not* type-enforced — pass them by hand.
- Init is bundled & synchronous (`src/i18n/index.ts`, imported once in `main.tsx`); no Suspense, no async loading.
- Plurals via `key_one`/`key_other` + `t(key, { count })`; interpolation via `{{var}}` + `t(key, { var })`.
- Presentational `ui/` components stay translation-free — they take already-translated strings as props; `t()` is called at the route/feature level. (`sidebar.tsx` is the exception: it owns its own nav copy.)
- **Add a language**: copy `locales/en.ts` → `<lng>.ts` (same shape), register it in `index.ts` `resources`, add a switcher. No language detector is wired yet.
- Backend error prose (raw `error.message`) is **not** localizable from the frontend — only mapped status codes (403/404) are. True localized errors need backend error codes.
