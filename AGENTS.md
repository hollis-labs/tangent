# AGENTS.md — Tangent

## What is this and why

Tangent is an agent-summoned, app-sized interactive surface: a single localhost
binary that any agent (Claude Code, Nanite, Cursor, Codex, Gemini CLI — anything
that speaks the Envelope UI Protocol over MCP) can summon when chat is the wrong
shape for the work. Diff-approval queues, design-comp triage, screenshot
annotation, persistent forms, spreadsheet review, brainstorm iteration, guided
wizards — workflows that don't fit a chat transcript get their own browser
window. Tangent is "Mode 1" of the parent interactive-collaboration-system idea:
the *separate-window app surface*. It deliberately does not cover inline modals,
fast-triage popovers, spatial canvases, or chat-multiplexing — those are
handled elsewhere (Nanite). Tangent absorbed and superseded the earlier
Fast-Triage project, which is now archived.

Today Tangent runs as a Go HTTP server with an embedded Vite/React SPA, not yet
wrapped with Wails. The architecture is structured so the eventual Wails wrap is
mechanical (same Go server inside a shell, same SPA build).

## Where to start

- `cmd/tangent/main.go` — binary entry point: arg parsing, signal handling,
  `--migrate-only` / `--rollback-one` flags.
- `cmd/tangent-dump-types/main.go` — dumps the go-envelopes catalog as JSON;
  feeds the TypeScript codegen.
- `internal/server/` — `net/http` server on `:7842`, route registration, and the
  `//go:embed all:ui_dist` directive (`static.go`). The Vite build writes into
  `internal/server/ui_dist/` so the embed resolves relative to the package.
- `internal/mcp/` — MCP server (Streamable HTTP `/mcp` + legacy SSE `/sse`),
  built on the official MCP Go SDK.
- `internal/envelope/` — envelope dispatcher; validates and routes envelopes.
  `internal/envelope/extensions/triage.go` is the reference plugin-extension.
- `internal/room/` + `internal/db/` — per-room state and SQLite persistence
  (`golang-migrate` embedded migrations).
- `internal/ws/` and `internal/server/ws_*.go` — per-room WebSocket bridge.
- `ui/` — React 19 + TypeScript + Tailwind v4 + shadcn SPA (standard Vite layout).
- `scripts/generate-envelope-types.mjs` — envelope TypeScript codegen.
- `docs/architecture.md` — system shape and layers (read this first for design).
- `docs/developing.md` — contributor onboarding and toolchain.
- `docs/mcp-integration.md` — Claude Code / Cursor / Codex / curl wiring recipes.
- `.agents/skills/` — repo-local workflow launcher skills (copy/paste known-good
  Tangent prompts and payload shapes for each bundled workflow).

## Key domain concepts

- **Envelope UI Protocol.** One shared envelope schema describes a unit of
  interactive work. Envelope types come from the `hollis-labs/go-envelopes`
  shared library (~26 core kinds inherited at v0.1); Tangent extends the catalog
  with its own kinds via the plugin-extension API rather than forking the
  registry. A React component registry maps `envelope.type` → component.
- **Two transports, one schema.** MCP (Streamable HTTP + SSE) is the portable
  lowest-common-denominator any agent can drive. A Nanite-native side-channel
  (mid-turn event injection) is a premium tier, planned post-v0.5.
- **Rooms.** Each agent session that summons Tangent gets its own room — own
  window, own WebSocket, own state — exposed at `/r/<roomID>`. Multi-agent
  concurrency is first-class: several agents can have active windows at once
  without state bleed. One room owns exactly one active WebSocket and one active
  pending envelope at a time; history persists.
- **Phase substrate.** Rooms carry workflow-neutral phase state (`current_phase`,
  `phases_visited`, `phase_outputs`). Workflows like the writing flow use a
  canonical sequence (`interview → synthesis → drafting → revision → output`)
  with explicit jump-backs. `tangent.session_get` projects per-workflow views.
- **Persistence.** SQLite at `~/.tangent/tangent.db` (`TANGENT_DB_PATH`
  overrides). Rooms and resolved envelope history survive a process restart.

## Common operations

```bash
# One-time setup
mise install                  # pins Go 1.26.1 + Node 22.12.0
make install-hooks             # install lefthook git hooks
cd ui && npm install && cd ..

# Build (frontend codegen → Vite build → embed → Go binary)
make build                     # produces ./tangent

# Dev (Go server on :7842 proxies non-API routes to Vite on :5173)
make dev

# Lint + test
make lint                      # gofmt + go vet + golangci-lint + biome
make test                      # go test -race + vitest

# Database
make db-migrate                # apply local SQLite migrations and exit
make db-rollback               # roll back the most recent migration

# Envelope codegen
make generate-envelopes        # regenerate ui/src/generated/envelope-types.ts
make check-envelopes           # CI gate: fail if committed types are stale

# Run
./tangent                      # serves http://localhost:7842/ (TANGENT_HTTP_PORT overrides)
```

Wire into Claude Code:

```bash
claude mcp add --transport http tangent http://localhost:7842/mcp
# or, if --transport http is rejected:
claude mcp add --transport sse tangent http://localhost:7842/sse
```

Then ask the agent to invoke a bundled workflow tool (`tangent.triage`,
`tangent.feedback`, `tangent.form-collect`, `tangent.design-iteration`,
`tangent.whiteboard`, `tangent.spreadsheet-review`, `tangent.approval-queue`,
`tangent.diff-review`, `tangent.file-picker`, `tangent.progress-panel`,
`tangent.dashboard`, `tangent.wizard`) or a writing-flow tool
(`tangent.session_*`, `tangent.interview_question`, `tangent.synthesis_notes`,
`tangent.block_draft`, `tangent.prose_revision`, `tangent.output_render`).
Tangent logs a room URL (`http://localhost:7842/r/<roomID>`); open it in a
browser, resolve the workflow, and the agent receives a structured response.

## Where to look for more

- `docs/architecture.md` — system shape, layers, persistence projections, Wails
  note, sandboxing model for `design-iteration`, multi-room concurrency.
- `docs/developing.md` — contributor toolchain and onboarding.
- `docs/mcp-integration.md` — full transport wiring for Claude Code, Cursor,
  Codex, plus curl verification and troubleshooting.
- `docs/manual-tests/` — per-workflow smoke-test and e2e recipes
  (`workflow-smoke-tests.md` is the quick pass across every shipped workflow).
- `docs/migrating-from-fast-triage.md` — migration path for existing Fast-Triage
  setups (Fast-Triage is migrated and archived).
- `CHANGELOG.md` — detailed per-release notes.
- Project knowledge file: `~/dev/agent-os/knowledge/projects/tangent.md` —
  full concept, lineage, composition map, open ideas, roadmap.
- Tracking / session notes: `~/dev/agent-os/workspaces/execution/tangent/` —
  per-version session ledgers (`v01/` … `v12/`, `v0-bootstrap/`).
- No ADRs directory and no `docs/roadmap.md` exist yet; roadmap lives inline in
  `README.md` and the knowledge file.

## Conventions

- Module path `github.com/hollis-labs/tangent`; license MIT.
- Go pinned to portfolio baseline 1.26.1 (matches `go-envelopes`); Node 22.12.0.
- `hollis-labs/go-envelopes` is a private module — CI authenticates with an
  org-level `GH_PAT` secret and sets `GOPRIVATE`/`GONOSUMDB`.
- Keep the HTTP layer separate from app logic so the future Wails wrap stays
  mechanical.
- Frontend deps stay minimal (React 19, Tailwind v4, Radix, Zustand, Zod);
  editor/chart/DnD libs get added only when a real workflow needs them.
- Two-root contract: code + tests live in this repo; tracking, backlog items,
  and session notes go in `~/dev/agent-os/workspaces/execution/tangent/`.
