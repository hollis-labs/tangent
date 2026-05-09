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

v0.2.0 released. Tangent now ships persistent rooms, multi-envelope sessions, and three bundled workflows: `tangent.triage`, `tangent.feedback`, and `tangent.design-iteration`. Fast-Triage has been migrated and archived; if you are moving an existing setup, see [`docs/migrating-from-fast-triage.md`](./docs/migrating-from-fast-triage.md). See [`CHANGELOG.md`](./CHANGELOG.md) for the full release entry.

Tangent today is a Go HTTP server with an embedded Vite SPA, not yet wrapped with Wails. Wails wrapping is deferred until the embedded-SPA pattern proves out elsewhere; the architecture is structured to make that future wrap mechanical (see [`docs/architecture.md`](./docs/architecture.md)).

## Quickstart

Install:

```bash
go install github.com/hollis-labs/tangent/cmd/tangent@v0.2.0
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

Then in any Claude Code session: ask Claude to use the `tangent.triage` tool. Tangent logs a room URL like `http://localhost:7842/r/<roomID>` — open it in a browser, decide each item, click Submit, and Claude receives the structured response.

Rooms now persist across server restart in `~/.tangent/tangent.db`, so a
resolved session history survives a process bounce.

For Cursor, Codex, the curl verification, and troubleshooting, see [`docs/mcp-integration.md`](./docs/mcp-integration.md).

### What changed since v0.1

- Rooms now persist in SQLite and survive restart.
- Tangent supports multi-envelope rooms through `tangent.session_*`.
- Two workflows joined `triage`: `feedback` and `design-iteration`.
- The SPA now includes a tab strip for switching active rooms.
- Fast-Triage has been retired; use Tangent and the migration guide above.

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

### v0.3 — Visual / design kinds

Add `mockup-board`, `comparison-split`, `annotated-image`, and `design-iteration` envelope kinds. These get contributed back to the core `go-envelopes` catalog so other hosts (Nanite, etc.) can render them too.

The next phase is the Interview Protocol writing variant: more deliberate
multi-step room workflows on top of the persistence and session substrate
shipped in v0.2. The full forward outline lives outside the repo in the
execution planning workspace; this README stays focused on the shipped app.

### v0.4 — New workflows

Spreadsheet review, form collect, approval queue, diff review, file picker, progress panel, dashboard, and wizard.

### v0.5 — Nanite-native side-channel

The premium transport tier with mid-turn event injection.

### v0.6+ — Distribution and trust

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

## License

MIT — see [LICENSE](./LICENSE).
