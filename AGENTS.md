# yivi-businesswallet

**Yivi** is a privacy-friendly identity wallet: people hold attribute-based credentials
on their phone and disclose the minimum a verifier asks for. It is mid-migration from the
original IRMA protocol to the European Digital Identity (EUDI) standards — OpenID4VCI,
OpenID4VP, SD-JWT VC — with both live side by side.

This repo is the **business wallet** built on that layer: a multi-tenant SaaS wallet where
organisations are tenants, members join through an invitation lifecycle, every mutation
lands in a per-org audit log, and an org holds credentials and co-signs documents itself.
It is a product heading for customers, not a demo.

`CLAUDE.md` is a symlink to this file.

## Parts

- `backend/` — Go (stdlib `net/http`) API. `cmd/api` serves and never migrates,
  `cmd/migrate` applies the goose migrations, `cmd/seed` seeds dev data; the domain slices
  live under `internal/`.
- `frontend/` — React 19 + Vite + TypeScript + react-router.
- `regulation/` — the EU compliance source features are written against.
- `dev-setup/` + `compose*.yaml` — the Docker dev stack. `npm run dev` from the repo root
  starts all of it (`npm run dev:reset` wipes the DB volumes first). Go 1.26+, Node 26+.

## Repos a change here touches

- **`privacybydesign/irmago`**, two independent ways, and only one of them is visible in a
  diff: a direct Go module dependency, and an HTTP boundary. The backend is a *requestor*:
  it starts an OpenID4VP presentation at a **hosted, remote** EUDI verifier
  (`EUDI_VERIFIER_URL`, default Yivi staging) and polls it by session id. No local daemon
  and no tunnel, so testing a login flow means scanning with a wallet holding
  `pbdf-staging.*` credentials.
- **`privacybydesign/openid4vc-poc-ops`** deploys that verifier, and it moves under us: a
  verifier upgrade has failed an untouched `main` before.

## Verify before finishing (what CI runs)

Run commands from `backend/` or `frontend/`, never the repo root.

```bash
cd frontend && npm run format && npm run lint && npm run typecheck && npm run build && npm test
cd backend && gofmt -l . && go vet ./... && go build ./... && go tool golangci-lint run ./... && go test -race ./...
```

## General conventions

No magic values. The formatter is authoritative — never hand-format. No new lint disables
without an inline reason. Fix the root cause; never swallow an error. Keep changes scoped,
and match the patterns of the file you are editing.

## Where the operational knowledge is

Not in this file. Load per area:

| Area | File |
|---|---|
| Backend (Go) conventions | `.ai/conventions/BACKEND.md` |
| Frontend (React/TS) conventions | `.ai/conventions/FRONTEND.md` |
| An existing capability | `.ai/features/<name>.md` |
| Planning and review | `.ai/plans/README.md` |
| Feature and regulatory scope | `regulation/FEATURE_LIST.md` |
| Running the stack | `README.md` |

Durable knowledge lands in `.ai/`, through the Harvest step in `.ai/plans/README.md`, not
here. The corpus this file used to be is in git history: 49,910 bytes at `3207652`, the
last revision carrying it (`git show 3207652:AGENTS.md`). A comment elsewhere in the repo
that says "see AGENTS.md" or "see CLAUDE.md" for a gotcha means that revision and not this
file. `frontend/src/lib/agents-md.test.ts` holds this file to 4,000 bytes.
