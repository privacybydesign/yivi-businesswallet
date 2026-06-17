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
- All backend calls go through `src/api/` clients — never `fetch` directly from components.
- API base URL comes from `import.meta.env.VITE_API_BASE_URL ?? ""` (empty string = use the Vite dev-server proxy). Don't hardcode hosts.
- Type API responses as named exported types (e.g. `HealthResponse`) and throw on non-`ok` responses (see `src/api/client.ts`).
- File names: kebab-case.
- Organize by feature as the app grows; co-locate route + its API client concerns where it reads cleanly.
