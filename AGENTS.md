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

Today Tangent runs as a Go HTTP server with an embedded Vite/React SPA served
in an ordinary browser. **There is no Wails, no desktop shell, and no
Nanite-native channel in the tree** — those are future-facing, and any document
that describes them as current is wrong. The architecture is structured so an
eventual Wails wrap stays mechanical (same Go server inside a shell, same SPA
build), which is a deferral, not a dependency.

The boundary Tangent refuses to cross is
[`docs/adr/0005-product-boundary-and-portfolio-composition.md`](docs/adr/0005-product-boundary-and-portfolio-composition.md).
Read it before proposing that Tangent own something new.

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
  `internal/envelope/extensions/triage.go` is the reference plugin-extension,
  and `internal/envelope/extensions/register_all.go` is the single table that
  binds every wire name. Authored manifests and schemas live under
  `internal/envelope/extensions/packages/<package-id>/<kind>/`.
- `internal/definition/` — immutable versioned definition manifests, retained
  material, and pinned replay.
- `internal/interaction/` + `internal/roomflow/` — the canonical durable
  substrate and the compatibility adapter every room workflow routes through.
- `internal/room/` + `internal/db/` — per-room state and SQLite persistence
  (`golang-migrate` embedded migrations). `internal/room/connection.go` owns
  the multi-connection lifecycle.
- `internal/authz/` + `internal/participant/` — the capability matrix and
  browser participant sessions.
- `internal/effect/` — host-mediated effect broker (declared, not exercised;
  see "Honest limitations" below).
- `internal/health/` + `internal/telemetry/` — liveness/readiness/capability
  probes and payload-safe correlated audit events.
- `internal/smoke/` — the operability check that derives the MCP surface from
  the shipped binary instead of from a number written down.
- `internal/ws/` and `internal/server/ws_*.go` — per-room WebSocket bridge.
- `ui/` — React 19 + TypeScript + Tailwind v4 + shadcn SPA (standard Vite layout).
- `scripts/generate-envelope-types.mjs` — envelope TypeScript codegen.
- `docs/architecture.md` — system shape and layers (read this first for design).
- `docs/developing.md` — contributor onboarding and toolchain.
- `docs/mcp-integration.md` — Claude Code / Cursor / Codex / curl wiring recipes.
- `.agents/skills/` — repo-local workflow launcher skills (copy/paste known-good
  Tangent prompts and payload shapes for bundled workflows and the durable
  `tangent-hitl-inbox` flow).

## Key domain concepts

- **Envelope UI Protocol.** One shared envelope schema describes a unit of
  interactive work. Core envelope types come from the `hollis-labs/go-envelopes`
  shared library; Tangent extends the catalog with its own kinds via the
  plugin-extension API rather than forking the registry. A React component
  registry maps `envelope.type` → component. The shipped process logs both
  halves at startup (`loaded envelope types` for the core catalog, `registered
  tangent envelope extensions` for Tangent's), and
  `ui/src/generated/envelope-types.ts` carries the same split in its generated
  header — read the count there, never from prose.
- **One transport today, one schema.** MCP (Streamable HTTP `/mcp` + legacy
  `/sse`) is the portable lowest-common-denominator any agent can drive, and it
  is the only agent-facing transport that exists. A Nanite-native side-channel
  (mid-turn event injection) remains an intended future tier with no
  implementation in the tree and no committed release.
- **Rooms are a compatibility surface projection, not an agent session.** A room
  (`/r/<roomID>`) is how the pre-substrate workflow tools project a durable
  *surface* into a browser; the canonical record is the interaction, and the
  `rooms`/`envelopes` tables are a projection of it, never a terminal-state
  authority. Nothing binds a room to one agent session, one caller, or one
  browser tab. Several callers can hold live rooms concurrently without state
  bleed, and a single room accepts **many simultaneous connections**: exactly
  one holds the resolver lease and may produce a terminal outcome, the rest
  observe. History persists across restart.
- **Resumable completion.** A room workflow's wait is not the work. A wait
  answered within 45 seconds returns the historical inline response; past that
  it returns a *successful pending receipt* carrying a durable handle, never an
  error and never a cancellation. Transport loss, caller timeout, browser
  disconnect, and process restart stop only the waiter. See
  `docs/room-workflow-completion.md`.
- **Authority is a session, not a URL.** A room URL is a locator. Browser
  authority lives in an `HttpOnly` participant session; caller authority is a
  host-assigned `<authority>:<partition>` scope over a seven-capability matrix
  (`view`, `submit`, `draft`, `resolve`, `cancel`, `close`, `administer`). See
  [ADR 0004](docs/adr/0004-caller-participant-and-room-access-authority.md).
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
make verify-supported          # test + lint + check-envelopes under the pinned Node
make smoke                     # boot the shipped binary; derive and check the MCP surface
TANGENT_SMOKE_ENV=1 make smoke # …plus the live deployment, Cerberus, and the Tether gateway

# Database
make db-migrate                # apply local SQLite migrations and exit
make db-rollback               # roll back the most recent migration
./tangent --db-check           # integrity, schema, guards, storage, ownership
./tangent --retention-plan     # what is past its retention window

# Envelope codegen
make generate-envelopes        # regenerate ui/src/generated/envelope-types.ts
make check-envelopes           # CI gate: fail if committed types are stale

# Run
./tangent                      # serves http://localhost:7842/ (TANGENT_HTTP_PORT overrides)

# Operability (three probes, three questions)
curl -fsS localhost:7842/healthz              # liveness — touches nothing
curl -fsS localhost:7842/readyz | jq .        # readiness — db, schema, registry, renderer, delivery
curl -fsS localhost:7842/healthz/capability   # which kinds can be served
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

Beyond those, the surface carries the generic durable substrate
(`tangent.surface_*`, `tangent.interaction_*`), the definition-registry
diagnostics (`tangent.definition_registry_list`, `tangent.definition_get`,
`tangent.definition_registry_diagnostics`), and the operability probes
(`tangent.health_report`, `tangent.telemetry_query`,
`tangent.retention_status`).

**Do not write the tool count anywhere.** It has been wrong in this repository
six times in one day. `make smoke` derives it by asking the shipped binary, and
`internal/smoke/docs_test.go` fails when a document disagrees with the build.

For one durable asynchronous approval or persistent-attention request, use the
repo-local `tangent-hitl-inbox` skill and the four `tangent.hitl_*` tools. They
return a stable handle immediately and use the operator-owned `/hitl` surface;
they do not create a room or replace the separate `tangent.approval-queue`
batch workflow.

## Where to look for more

- `docs/architecture.md` — system shape, layers, persistence projections,
  connection lifecycle, authorization, renderer trust classes, host-mediated
  capabilities, health, telemetry, the Wails deferral, and the canonical
  **Current limitations** list.
- `docs/adr/` — five accepted decision records: `0001` lifecycle boundaries,
  `0002` retention and draft custody, `0003` definition and package ownership,
  `0004` caller/participant/room access authority, `0005` product boundary and
  portfolio composition. `0005` retired `docs/interactive-collaboration-direction.md`;
  that file is deleted and must not be re-created.
- `docs/database-operations.md` — ownership, backup/restore/repair, and the six
  deletion kinds.
- `docs/host-mediated-capabilities.md` — the two capability namespaces and what
  is enforced versus merely declared.
- `docs/renderer-trust-classes.md` — the five trust classes, CSP, and sandboxing.
- `docs/room-workflow-completion.md` — resumable completion and the pending
  receipt.
- `docs/mcp-smoketest.md` — the four MCP failure modes and the `make smoke`
  operator recipe.
- `docs/developing.md` — contributor toolchain and onboarding.
- `docs/mcp-integration.md` — full transport wiring for Claude Code, Cursor,
  Codex, plus curl verification and troubleshooting.
- `docs/manual-tests/` — per-workflow smoke-test and e2e recipes
  (`workflow-smoke-tests.md` is the quick pass across every shipped workflow).
- `docs/migrating-from-fast-triage.md` — migration path for existing Fast-Triage
  setups (Fast-Triage is migrated and archived).
- `CHANGELOG.md` — detailed per-release notes.
- Tracking / session notes: `~/dev/agent-os/workspaces/execution/tangent/` —
  one directory per Torque task id (`CW-…`). Drafts awaiting review go in
  `~/dev/agent-os/workspaces/drafts/tangent/`.
- There is no `docs/roadmap.md` and no project knowledge file at
  `~/dev/agent-os/knowledge/projects/tangent.md` — earlier revisions of this
  document pointed at both and neither exists. The roadmap lives inline in
  `README.md`; open work lives in Torque under project `PRJ-20260825-0002`.
- `.agent-ops/project.yaml` is the machine-readable answer to "who starts
  Tangent here, and where does its state live".

## Honest limitations

These are current, verified, and deliberately load-bearing. A document that
omits them is the failure mode the retired direction document died for. The
canonical list with full context is
[`docs/architecture.md`](docs/architecture.md#current-limitations); the
headlines:

- **`standalone-local` partitions are advisory, not a security boundary.** Any
  local caller can assert any partition. Enforcement exists only *across*
  authorities. Nothing downstream may present a partition as isolation.
- **No manifest declares a host-mediated effect capability**, so every request
  through the effect broker refuses `effect_capability_undeclared`. The broker
  has zero production traffic (`CW-20260905-0010`).
- **`clipboard.write` and `export.download` are enforced only inside a
  sandboxed frame.** On the main origin they remain declared-not-enforced.
  `network.fetch` is genuinely enforced by the document CSP.
- **There is no browser in CI.** CSP and sandboxing are proven by construction,
  not observed; `docs/manual-tests/renderer-sandbox-e2e.md` is the real
  verification (`CW-20260904-0171`).
- **The OpenTelemetry path has never been observed against a collector**
  (`CW-20260905-0011`).
- **`SaveDraft` has no production caller** — browser `localStorage` is the only
  running draft custody (`CW-20260905-0001`).
- **`renderer.entry` loads nothing**; `ui/src/main.tsx` registers renderers by
  string literal (`CW-20260905-0004`).
- **The ADR 0002 §3 custody-precedence engine is not implemented**; retention
  uses host windows only (`CW-20260905-0008`).

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
