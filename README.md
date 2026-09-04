# Tangent

An agent-summoned, app-sized interactive surface. Tangent is a single installable desktop app that any agent — Claude Code, Nanite, Cursor, Codex, Gemini CLI, or anything else that speaks the Envelope UI Protocol — can summon when chat is the wrong shape for the work.

Use cases the chat window can't carry well:

- diff-approval queues
- design-comp triage
- dashboard snapshots
- screenshot annotation
- persistent forms
- brainstorm iteration
- spreadsheet review
- progress tracking

Tangent is the *separate-window app surface* for an interactive collaboration system. Inline modals, fast-triage popovers, spatial canvases, and chat-multiplexor modes are deliberately handled elsewhere — keeping Tangent's scope to a single shape is intentional.

## Status

The latest published tag is `v0.12.0`. This release adds
`tangent.wizard`: a room-backed guided wizard workflow with canonical
step progress, explicit partial updates, branch selections, local draft
recovery, review summary, and final completion. It also tightens the
manual operator surface with repo-local workflow skills under
`.agents/skills/` plus clearer smoke-test guidance for payload-sensitive
workflows. Existing `tangent.dashboard` support remains in place for
workflow-state snapshots and drill-down. Fast-Triage has been migrated
and archived; if you are moving an existing setup, see
[`docs/migrating-from-fast-triage.md`](./docs/migrating-from-fast-triage.md).
See [`CHANGELOG.md`](./CHANGELOG.md) for the detailed release notes.

Tangent today is a Go HTTP server with an embedded Vite SPA, not yet wrapped with Wails. Wails wrapping is deferred until the embedded-SPA pattern proves out elsewhere; the architecture is structured to make that future wrap mechanical (see [`docs/architecture.md`](./docs/architecture.md)).

## Quickstart

Install:

```bash
go install github.com/hollis-labs/tangent/cmd/tangent@v0.12.0
```

Run:

```bash
tangent
# tangent listening addr=:7842
# MCP server ready http_url=http://localhost:7842/mcp sse_url=http://localhost:7842/sse
```

Wire it into Claude Code (verified against `claude` CLI as of 2026-05-08):

```bash
claude mcp add --transport http tangent http://localhost:7842/mcp
```

If your `claude` rejects `--transport http`, fall back to SSE:

```bash
claude mcp add --transport sse tangent http://localhost:7842/sse
```

Then in any Claude Code session: ask Claude to use either one of the bundled workflow tools (`tangent.triage`, `tangent.feedback`, `tangent.form-collect`, `tangent.design-iteration`, `tangent.whiteboard`, `tangent.spreadsheet-review`, `tangent.approval-queue`, `tangent.diff-review`, `tangent.file-picker`, `tangent.progress-panel`, `tangent.dashboard`, `tangent.wizard`) or the writing flow tools (`tangent.session_*`, `tangent.interview_question`, `tangent.synthesis_notes`, `tangent.block_draft`, `tangent.prose_revision`, `tangent.output_render`). Tangent logs a room URL like `http://localhost:7842/r/<roomID>` — open it in a browser, resolve the workflow, and Claude receives the structured response.

For durable asynchronous approvals or persistent-attention items, use the four
`tangent.hitl_*` tools and the operator-owned `/hitl` inbox. The repo-local
[`tangent-hitl-inbox` skill](./.agents/skills/tangent-hitl-inbox/SKILL.md)
provides the handle-first flow and known-good direct Tangent and Tether
native-flat payloads. This is separate from the room-backed
`tangent.approval-queue` batch workflow.

Rooms now persist across server restart in `~/.tangent/tangent.db`, so a
resolved session history survives a process bounce.

For Cursor, Codex, the curl verification, and troubleshooting, see [`docs/mcp-integration.md`](./docs/mcp-integration.md).

### What v0.12.0 adds

- Tangent now ships `tangent.wizard`, a room-backed guided wizard workflow with explicit partial updates and final completion.
- Wizard turns persist canonical step definitions, accepted step progress, branch selections, and summary fields under `session_get.wizard`.
- The browser host now supports local draft recovery, review summary, forward/back navigation, attachment-style responses, and action outputs without introducing any cloud sync or multi-user state.
- The repo now includes tight workflow launcher skills under `.agents/skills/` so operators can copy/paste known-good Tangent prompts and payload shapes.
- Manual smoke-test docs now call out payload-sensitive workflows and the one-tab-per-room constraint for pending submissions.

For the full shipped behavior, see [`CHANGELOG.md`](./CHANGELOG.md).

## How it works

**Stack.** Wails (Go backend + React / TypeScript / Tailwind v4 / shadcn frontend), shipping as a single binary with system-tray integration.

**Two transports, one envelope schema.** Tangent supports two ways for an agent to drive a window, both speaking the same envelope shape:

- **MCP (Streamable HTTP + SSE)** — the portable lowest-common-denominator. Any MCP-speaking agent can launch Tangent workflows and receive structured responses.
- **Nanite-native side-channel** — a premium tier available when Tangent is launched as a managed child of a Nanite session. Adds mid-turn event injection on top of the same envelope schema.

**Per-session rooms.** Each agent session gets its own window and state. Multi-agent concurrency is the default, not an edge case — several agents can have active Tangent windows at once without bleeding state across them.

**Envelope renderer.** A React component registry maps each envelope `type` to a component. The registry is shared with Nanite rather than re-implemented, so Tangent inherits its existing envelope kinds.

**Persistence.** Configurable per envelope kind (markdown, diffs, screenshots, design comps, etc.). The defaults are config-overridable, and users can flip persistence per instance.

## Roadmap

The phases below are illustrative — they sketch the intended shape of releases, not a contract.

### v0.1 — Prove the shape

Wails shell, MCP server, one bundled workflow (triage). The goal is end-to-end: any MCP-speaking agent can launch a rich workflow in Tangent and receive a structured response back.

### v0.4 — Shared whiteboard

Shipped. Tangent now includes a persistent, MCP-driven whiteboard workflow for layout, annotation, and spatial collaboration. The v0.4 whiteboard is single-user localhost first, shared between user and agent through one room, backed by tldraw scene JSON, explicit submit, local refresh recovery, PNG export, and revision browsing.

### v0.5 — Spreadsheet review

Shipped. Tangent now includes a persistent, MCP-driven spreadsheet-review workflow for dense row review, canonical query state, saved views, local refresh recovery, CSV export metadata, and explicit submit in one room.

### v0.6 — Generalized form collect

Shipped. Tangent now includes a persistent, MCP-driven schema-first form workflow for rich field collection, conditional sections, repeatable groups, room-backed drafts/templates, attachment refs, and durable submission summaries.

### v0.7 — Approval queue

Shipped. Tangent now includes a room-backed approval queue for serialized review with evidence panes, durable decision history, defer reasons, batch controls, audit export metadata, and explicit submit/reopen turns.

### v0.8 — Diff review

Shipped on the current branch line before file-picker. Tangent added a room-backed diff review workflow with per-file/per-hunk decisions, durable comments, artifact-backed before/after refs, batch review controls, draft recovery, and exportable summaries.

### v0.9 — File picker

Shipped. Tangent added a room-backed file-picker workflow with allowed local browse roots, persisted query state, browser-local draft recovery, accepted selection revision history, and reusable artifact-ref handoff payloads.

### v0.10 — Progress panel

Shipped. Tangent added a room-backed progress workflow with append-only updates, timeline/checkpoint/log inspection, local operator-context recovery, and concise summary/export inspection.

### v0.11 — Dashboard

Shipped. Tangent adds a room-backed dashboard workflow that summarizes Tangent room/workflow state through reusable saved layouts, explicit refresh/update submits, room/artifact drill-down, and deterministic export metadata.

### v0.12 — Wizard

Current branch. Tangent adds a room-backed guided wizard workflow for bounded step progression, branch selections, explicit partial saves, local draft recovery, and final review/completion.

### v0.8+ — Distribution and trust

Installation, capability gating, isolation, and the trust model for third-party workflow plugins.

## Composition

Tangent is built to compose with the rest of the `hollis-labs` Go ecosystem rather than re-implement equivalents.

**Depends on:**

- `hollis-labs/go-envelopes` — Go envelope schemas, manifest, and plugin extension API.
- `ts-envelopes` — the TypeScript counterpart, once it lands.

**Reuses:**

- Nanite's React envelope component registry. Because the registry is shared, Tangent inherits roughly 26 envelope kinds for free at v0.1 — approval-card, diff-card, document-viewer, question-form, table-card, and the rest.

**Likely to compose with:**

- `go-plugin` — plugin SDK.
- `go-strutil` — slugify, namespace validation, and similar string utilities.
- `go-runner` — subprocess management if Tangent ever spawns helpers.
- `go-mcp` — MCP server implementation.

Where one of these libraries already covers a need, Tangent should reach for it before writing an equivalent.

## Non-goals

A few things Tangent deliberately is not, to keep scope honest:

- **Not a chat runtime.** That's Nanite. Tangent is the separate-window surface — same envelope schema, complementary host.
- **Not a build-time codegen tool.** That's Sigil. Tangent renders envelopes at runtime through the React registry.
- **No central registry, cloud service, or auth in v1.** Single-user, localhost only.

## Development

Prerequisites:

- Go 1.26.1 (matches `go-envelopes`)
- Node 22.12.0 (`mise.toml` pins the repo toolchain)
- `lefthook` (`brew install lefthook`) for the pre-commit / pre-push hooks

```bash
# One-time
mise install
make install-hooks
cd ui && npm install && cd ..

# Build (frontend → embedded into Go binary)
make build           # produces ./tangent

# Dev (Go server proxies non-API requests to Vite at :5173)
make dev

# Lint + test
make lint
make test            # go test -race + vitest

# Run the binary
./tangent            # serves on :7842 (override via TANGENT_HTTP_PORT)
```

More docs:

- [`docs/architecture.md`](./docs/architecture.md) — system shape and layers
- [`docs/developing.md`](./docs/developing.md) — contributor onboarding and toolchain setup
- [`docs/mcp-integration.md`](./docs/mcp-integration.md) — Claude Code / Cursor / curl recipes
- [`docs/manual-tests/workflow-smoke-tests.md`](./docs/manual-tests/workflow-smoke-tests.md) — quick smoke pass across every shipped workflow
- [`docs/manual-tests/triage-e2e.md`](./docs/manual-tests/triage-e2e.md) — full e2e recipe
- [`docs/manual-tests/feedback-e2e.md`](./docs/manual-tests/feedback-e2e.md) — full feedback workflow
- [`docs/manual-tests/design-iteration-e2e.md`](./docs/manual-tests/design-iteration-e2e.md) — full design-iteration workflow
- [`docs/manual-tests/writing-flow-e2e.md`](./docs/manual-tests/writing-flow-e2e.md) — full Interview Protocol writing workflow
- [`docs/manual-tests/whiteboard-e2e.md`](./docs/manual-tests/whiteboard-e2e.md) — full whiteboard workflow with autosave, export, and revision browser
- [`docs/manual-tests/spreadsheet-review-e2e.md`](./docs/manual-tests/spreadsheet-review-e2e.md) — full spreadsheet-review workflow with recovery, saved views, and CSV export metadata
- [`docs/manual-tests/form-collect-e2e.md`](./docs/manual-tests/form-collect-e2e.md) — full form-collect workflow with conditional sections, recovery, and attachment refs
- [`docs/manual-tests/approval-queue-e2e.md`](./docs/manual-tests/approval-queue-e2e.md) — full approval-queue workflow with reopen, defer reasons, and audit export metadata
- [`docs/manual-tests/hitl-inbox-e2e.md`](./docs/manual-tests/hitl-inbox-e2e.md) — durable HITL inbox with concurrent callers, evidence, reconnect, restart, and later handle retrieval
- [`docs/manual-tests/diff-review-e2e.md`](./docs/manual-tests/diff-review-e2e.md) — full diff-review workflow with reopen, batch decisions, and summary export
- [`docs/manual-tests/file-picker-e2e.md`](./docs/manual-tests/file-picker-e2e.md) — full file-picker workflow with reopen, local draft recovery, and artifact-ref handoff inspection
- [`docs/manual-tests/progress-panel-e2e.md`](./docs/manual-tests/progress-panel-e2e.md) — full progress-panel workflow with update/reopen, checkpoint inspection, and export snapshot verification
- [`docs/manual-tests/dashboard-e2e.md`](./docs/manual-tests/dashboard-e2e.md) — full dashboard workflow with saved layouts, drill-down, and export/share snapshot verification
- [`docs/manual-tests/wizard-e2e.md`](./docs/manual-tests/wizard-e2e.md) — full wizard workflow with partial saves, recovery, and final completion

## License

MIT — see [LICENSE](./LICENSE).
