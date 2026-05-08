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

Pre-implementation. The concept and protocol spec are drafted, but no code has been written yet. Implementation is gated on:

1. `hollis-labs/go-envelopes` shipping v0.1.0 (the shared envelope schema package), and
2. The Nanite chat runtime migrating to consume `go-envelopes`.

This initial commit is a placeholder so future phase branches have a base to PR against. Expect this README to be the only artifact in the repo until v0.1 work begins.

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

### v0.2 — Fast-Triage parity + archive

Bundle the `triage` and `feedback` workflows. Add per-session rooms (multi-tab), cancel handling, and verified multi-agent concurrency.

### v0.3 — Visual / design kinds

Add `mockup-board`, `comparison-split`, `annotated-image`, and `design-iteration` envelope kinds. These get contributed back to the core `go-envelopes` catalog so other hosts (Nanite, etc.) can render them too.

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

Setup, build, and contribute instructions will land with the v0.1 phase PR. This README is the bootstrap baseline.

## License

To be added in the v0.1 phase PR.
