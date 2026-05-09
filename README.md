# Tangent

An agent-summoned, app-sized interactive surface. Tangent is a single installable desktop app that any agent — Claude Code, Nanite, Cursor, Codex, Gemini CLI, or anything else that speaks the Envelope UI Protocol — can summon when chat is the wrong shape for the work.

Use cases the chat window can't carry well:

- diff-approval queues
- design-comp triage
- screenshot annotation
- persistent forms
- brainstorm iteration
- spreadsheet review

Tangent is the *separate-window app surface* for an interactive collaboration system. Inline modals, fast-triage popovers, spatial canvases, and chat-multiplexor modes are deliberately handled elsewhere — keeping Tangent's scope to a single shape is intentional.

## Status

v0.3.0 released. Tangent now ships persistent rooms, multi-envelope sessions, three general workflows (`tangent.triage`, `tangent.feedback`, `tangent.design-iteration`), and the first full Interview Protocol workflow for writing (`interview -> synthesis -> drafting -> revision -> output`). Fast-Triage has been migrated and archived; if you are moving an existing setup, see [`docs/migrating-from-fast-triage.md`](./docs/migrating-from-fast-triage.md). See [`CHANGELOG.md`](./CHANGELOG.md) for the full release entry.

Tangent today is a Go HTTP server with an embedded Vite SPA, not yet wrapped with Wails. Wails wrapping is deferred until the embedded-SPA pattern proves out elsewhere; the architecture is structured to make that future wrap mechanical (see [`docs/architecture.md`](./docs/architecture.md)).

## Quickstart

Install:

```bash
go install github.com/hollis-labs/tangent/cmd/tangent@v0.3.0
```

If you are reading this before the `v0.3.0` tag is published, use `@main`
temporarily and switch back to the release tag once it lands.

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

Then in any Claude Code session: ask Claude to use either one of the bundled workflow tools (`tangent.triage`, `tangent.feedback`, `tangent.design-iteration`) or the v0.3 writing flow tools (`tangent.session_*`, `tangent.interview_question`, `tangent.synthesis_notes`, `tangent.block_draft`, `tangent.prose_revision`, `tangent.output_render`). Tangent logs a room URL like `http://localhost:7842/r/<roomID>` — open it in a browser, resolve the workflow, and Claude receives the structured response.

Rooms now persist across server restart in `~/.tangent/tangent.db`, so a
resolved session history survives a process bounce.

For Cursor, Codex, the curl verification, and troubleshooting, see [`docs/mcp-integration.md`](./docs/mcp-integration.md).

### What changed since v0.2

- Tangent now ships the first full Interview Protocol workflow for writing.
- Rooms now track explicit workflow phases through `tangent.session_advance_phase` and `tangent.session_set_phase_output`.
- The writing flow adds five new bundled workflow tools: `tangent.interview_question`, `tangent.synthesis_notes`, `tangent.block_draft`, `tangent.prose_revision`, and `tangent.output_render`.
- `tangent.session_get` now exposes phase metadata, structured interview history, synthesis projection, accepted draft blocks, revision outcomes, and the final output artifact.
- The local Tangent skill/command docs now describe the canonical end-to-end writing choreography, including explicit jump-back from `revision` to `drafting`.

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

The next phase is a Tangent-native shared whiteboard workflow: a persistent, MCP-driven freeform canvas for layout, annotation, and spatial collaboration. The planned MVP is single-user localhost first, shared between user and agent through one room, likely using a tldraw-backed canvas.

### v0.5 — Additional workflow expansion

Spreadsheet review, form collect, approval queue, diff review, file picker, progress panel, dashboard, and wizard.

### v0.6 — Nanite-native side-channel

The premium transport tier with mid-turn event injection.

### v0.7+ — Distribution and trust

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
- [`docs/manual-tests/triage-e2e.md`](./docs/manual-tests/triage-e2e.md) — full e2e recipe
- [`docs/manual-tests/writing-flow-e2e.md`](./docs/manual-tests/writing-flow-e2e.md) — full Interview Protocol writing workflow

## License

MIT — see [LICENSE](./LICENSE).
