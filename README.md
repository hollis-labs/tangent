# Tangent

Tangent is an agent-summoned interactive surface: one Go binary, an MCP
server, and a React SPA embedded in it, opened in a browser (or the
`Tangent.app` desktop shell) when chat is the wrong shape for the work. Any
MCP-speaking agent — Claude Code, Nanite, Cursor, Codex, Gemini CLI — can
summon a room, hand it a decision, and get a structured response back.

> **Pre-release.** Tangent has tagged builds (latest **`v0.16.0`**) that its
> author runs day to day, but it is not a supported release and has no
> outside consumers. It's being built in the open: the code, the docs, and
> this README describe what exists today, not a pitch for what's planned.
> Interfaces and behavior change without notice, and there are no
> compatibility guarantees yet. Localhost and single-user only — see
> [Current limitations](docs/architecture.md#current-limitations) for what's
> enforced versus merely declared.

## What it is today

- **One transport, one envelope schema.** MCP (Streamable HTTP + legacy SSE)
  is the only agent-facing surface. A room workflow's structured response is
  the same envelope schema Nanite renders with.
- **Rooms, not sessions.** A room (`/r/<roomID>`) projects a durable
  *interaction* into a browser. Multiple tabs and callers can hold a room at
  once; exactly one holds the resolver lease.
- **One default Inbox** for approvals, documents, agent turns, and structured
  interactions, with FIFO arrival order, filters, and durable response history — a wait can return an
  inline answer or a pending receipt with a durable handle, and survives
  transport loss, timeouts, and process restart.
- **A desktop shell.** `Tangent.app` (Wails v3) wraps the same loopback
  server; unsigned and without a system tray yet.
- A catalog of room-backed workflows spanning triage, form collection,
  diff/spreadsheet review, approval queues, dashboards, and a shared
  whiteboard. The current set is what the running binary reports — see
  [`docs/mcp-integration.md`](docs/mcp-integration.md) rather than trusting a
  count here, since it drifts.

## Where it sits in the stack

```
   agents          Claude Code, Nanite, Cursor, Codex, Gemini CLI — anything
                    speaking MCP
        │  MCP (tool call in, envelope out)
   ┌──────────┐
   │ Tangent  │   room surface, envelope renderer, durable HITL inbox
   └──────────┘
        │  plugin (in-process MCP client, same authority as any local caller)
   Torque, Tesseract   plugins that sync a board or log a review —
                        Tangent's binary takes no dependency on either
```

Tangent is deliberately **not** a chat runtime (that's Nanite — Tangent
reuses its envelope component registry rather than re-implementing it) and
**not** a codegen tool (that's Sigil). It's the separate-window surface a
chat-shaped tool hands off to when the work needs a form, a queue, a diff, or
a spreadsheet instead of a scrollback.

## Examples

**Daily use.** Claude Code or Nanite calls a workflow tool (diff review,
approval queue, a form) mid-session; Tangent logs a room URL, the operator
opens it, resolves the workflow in the browser, and the agent gets a structured
result back — no context-switch into a different tool for the parts of the
job a chat window renders badly.

**Composition.** The Torque and Tesseract integrations are plugins, not core
features: the Torque plugin syncs a Tangent board to a Torque board through
Torque's own client and authority, and the Tesseract plugin does the same for
review write-back. Tangent's own binary knows nothing about either domain — a
plugin is the only thing that does, and the first-party plugins live in their
own repository, [`hollis-labs/tangent-plugins`](https://github.com/hollis-labs/tangent-plugins).

## Roadmap

- **Third-party workflow distribution.** Renderer trust classes (which
  decide where a definition's code runs) are shipped; capability gating and a
  trust model for externally authored workflow packages are not started.
- **Nanite-native side channel.** A second transport adding mid-turn event
  injection over the same envelope schema, for when Tangent runs as a managed
  child of a Nanite session. Direction only — nothing in the tree implements
  it yet.
- **Desktop shell hardening.** System tray, close-to-hide, single-instance
  UX, code signing, and notarization, on top of the unsigned shell that
  shipped in `v0.13.0`.

See [`CHANGELOG.md`](CHANGELOG.md) for what's shipped.

## License

MIT — see [LICENSE](LICENSE).


## Quickstart

Install by building from source. `go install` is not an install path: the web UI
is built by Vite and embedded at build time, and a module fetch carries only an
empty placeholder where it belongs, so the binary it produces serves no UI.

```bash
git clone https://github.com/hollis-labs/tangent.git && cd tangent
mise install            # pins Node; see mise.toml
(cd ui && npm ci)
make build              # → ./tangent, with the UI embedded
make install-plugins    # optional: first-party plugins at tangent-plugins.version
# `make build-app` additionally assembles Tangent.app on macOS.
```

Run:

```bash
./tangent
# tangent listening addr=127.0.0.1:7842
# MCP server ready http_url=http://127.0.0.1:7842/mcp sse_url=http://127.0.0.1:7842/sse
```

Wire it into Claude Code:

```bash
claude mcp add --transport http tangent http://127.0.0.1:7842/mcp
# or, if your claude rejects --transport http:
claude mcp add --transport sse tangent http://127.0.0.1:7842/sse
```

For Cursor, Codex, curl verification, and troubleshooting, see
[`docs/mcp-integration.md`](docs/mcp-integration.md). For durable
asynchronous approvals, see the `tangent.hitl_*` tools and
[`.agents/skills/tangent-hitl-inbox/SKILL.md`](.agents/skills/tangent-hitl-inbox/SKILL.md).

## Development

The protocol-2 plugin-host integration in this source branch is not activated. Existing protocol-1
plugins must be rebuilt; the GitHub plugin remains on protocol 1 pending its
separate migration. Source-only pseudo-version pins must be replaced by
approved releases before activation. This source change installs or deploys
nothing.

```bash
# after the Quickstart's clone and installs:
make install-hooks     # lefthook: pre-commit / pre-push
make build             # frontend → embedded into Go binary → ./tangent
make dev               # Go server proxies non-API requests to Vite at :5173
make lint              # gofmt + go vet + golangci-lint + biome
make test              # go test -race + vitest
make verify-supported  # test + lint + check-envelopes under the pinned Node
```

Database operations (`--db-check`, `--db-backup`, `--db-restore`,
`--db-repair`, `--retention-plan`, `--erase-interaction`) are documented in
[`docs/database-operations.md`](docs/database-operations.md).

Start with [`docs/inbox.md`](docs/inbox.md) for the operator guide.

More: [`docs/architecture.md`](docs/architecture.md) (system shape and the
canonical limitations list), [`docs/adr/`](docs/adr/) (accepted decision
records), [`docs/developing.md`](docs/developing.md) (contributor
onboarding), [`docs/installing.md`](docs/installing.md) (macOS install and
upgrade).
