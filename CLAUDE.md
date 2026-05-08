# Tangent

Tangent is a Go HTTP server with an embedded React/TypeScript SPA. v0.1
is the "prove the shape" milestone — a binary that builds, lints, tests,
and serves a placeholder page from an embedded Vite bundle. Subsequent
PRs add the WebSocket envelope channel, MCP tooling, and the real UI.

## Build & test quickstart

```bash
make install-hooks            # one-time: install lefthook git hooks
cd ui && npm install && cd .. # one-time: install frontend deps
make build                    # produces ./tangent (frontend + Go)
make lint                     # gofmt + go vet + golangci-lint + biome
make test                     # go test -race + vitest
./tangent                     # serves http://localhost:7842/
```

`make dev` runs the Vite dev server (port 5173) and the Go server (port
7842) in parallel. The Go server reverse-proxies non-API requests to
Vite when `TANGENT_DEV_FRONTEND_URL` is set; `make dev` sets it for you.

## Architecture (brief)

- `cmd/tangent/` — binary entry point; arg parsing, signal handling.
- `internal/server/` — HTTP server, route registration, embedded SPA.
  - `static.go` holds the `//go:embed all:ui_dist` directive; the
    Vite build (`ui/vite.config.ts`) writes its output to
    `internal/server/ui_dist/` so the embed directive resolves
    relative to the Go package.
- `ui/` — React + TypeScript + Tailwind v4 SPA. Standard Vite layout.
- `scripts/` — Node helpers (envelope type generation lands in PR 2).

A full architecture document lands in PR 6 at `docs/architecture.md`.

## Conventions

- License: MIT. Module path: `github.com/hollis-labs/tangent`.
- Go version: pinned to the portfolio baseline (1.26.1, matching
  `go-envelopes`).
- No Wails, no plugin sandbox, no MCP server in v0.1 — those are later
  PRs. Keep the HTTP layer separate from app logic so a Wails wrapper is
  mechanical to bolt on later.
- Frontend deps stay minimal: React 19, Tailwind v4, Radix (via the
  `radix-ui` meta-package), Zustand, Zod. Editor / chart / DnD libs get
  added when a real workflow needs them — not preemptively.

## Boot prompts and agent context

`.nanite/` lives at the workspace level (`agent-workspaces/.nanite/`)
not in this repo. Agents booted against Tangent should rely on the
two-root contract: code + tests live here; tracking, BLGs, and session
notes go in `agent-workspaces/execution/tangent/...`.
