# Tangent

Tangent is a Go HTTP server with an embedded React/TypeScript SPA that
any MCP-speaking agent can summon when chat is the wrong shape for the
work. It serves the MCP tool surface, the per-room browser workflows,
and the durable `/hitl` operator inbox from one binary on `:7842`.

**[`AGENTS.md`](./AGENTS.md) is the canonical agent context for this
repo.** It carries the current entry points, domain concepts, commands,
and conventions. This file exists for Claude Code's own loader and only
records what is specific to it; when the two disagree, `AGENTS.md` wins.

## Build & test quickstart

```bash
make install-hooks            # one-time: install lefthook git hooks
cd ui && npm install && cd .. # one-time: install frontend deps
make build                    # produces ./tangent (frontend + Go)
make lint                     # gofmt + go vet + golangci-lint + biome
make test                     # go test -race + vitest
make smoke                    # boot the shipped binary and check the MCP surface
./tangent                     # serves http://localhost:7842/
```

`make verify-supported` runs `test lint check-envelopes` under the pinned
Node runtime and is the gate to trust over a bare `make` invocation.

`make dev` runs the Vite dev server (port 5173) and the Go server (port
7842) in parallel. The Go server reverse-proxies non-API requests to
Vite when `TANGENT_DEV_FRONTEND_URL` is set; `make dev` sets it for you.

## Architecture (brief)

- `cmd/tangent/` — binary entry point; arg parsing, signal handling, and
  the one-off database/maintenance modes.
- `internal/server/` — HTTP server, route registration, embedded SPA.
  - `static.go` holds the `//go:embed all:ui_dist` directive; the
    Vite build (`ui/vite.config.ts`) writes its output to
    `internal/server/ui_dist/` so the embed directive resolves
    relative to the Go package.
- `internal/mcp/`, `internal/room/`, `internal/roomflow/`,
  `internal/interaction/`, `internal/definition/`, `internal/authz/`,
  `internal/effect/`, `internal/health/`, `internal/telemetry/` — see
  [`docs/architecture.md`](./docs/architecture.md) for what each owns.
- `ui/` — React + TypeScript + Tailwind v4 SPA. Standard Vite layout.
- `scripts/` — Node helpers, including the envelope type codegen.

[`docs/architecture.md`](./docs/architecture.md) is the full document, and
[`docs/adr/`](./docs/adr/) carries the five accepted decision records.

## Conventions

- License: MIT. Module path: `github.com/hollis-labs/tangent`.
- Go version: pinned to the portfolio baseline (1.26.1, matching
  `go-envelopes`); Node 22.12.0.
- Keep the HTTP layer separate from app logic. A Wails desktop wrap is a
  future migration, not a current dependency — nothing in the tree is
  Wails today.
- Frontend deps stay minimal: React 19, Tailwind v4, Radix (via the
  `radix-ui` meta-package), Zustand, Zod. Editor / chart / DnD libs get
  added when a real workflow needs them — not preemptively.
- **Never write a tool count, envelope-kind count, or workflow count into
  prose without a test that fails when it drifts.** That number has been
  wrong in this repository more often than right. `make smoke` derives it
  from the shipped build; `internal/smoke/docs_test.go` asserts that the
  documents agree with what the binary advertises.

## Boot prompts and agent context

This repo carries no `.nanite/` directory, and none exists at any
workspace root — the older pointer to `agent-workspaces/.nanite/` was
wrong. Agents booted against Tangent rely on the two-root contract: code
and tests live here; tracking, plans, and session notes go in
`~/dev/agent-os/workspaces/execution/tangent/`, and drafts under review
go in `~/dev/agent-os/workspaces/drafts/tangent/`.
