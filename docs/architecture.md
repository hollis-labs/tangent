# Tangent architecture (v0.2)

A one-pager. For the user-facing setup recipe, see
[`mcp-integration.md`](./mcp-integration.md). For contributor onboarding,
see [`developing.md`](./developing.md).

## Shape

```
                  ┌────────────────────────────────────────────────┐
                  │              Tangent (single binary)           │
                  │                                                │
   Browser  ◀──── │  HTTP server :7842                             │
   (SPA at /r/.) ─│   ├── Embedded Vite SPA  (go:embed ui_dist)    │
                  │   ├── WebSocket bridge   (/ws, per-room state) │
                  │   └── MCP server         (/mcp + /sse)         │
                  │                │                               │
                  │                ▼                               │
                  │         Envelope dispatcher                    │
                  │                │                               │
                  │                ▼                               │
                  │         go-envelopes registry                  │
                  │           + plugin extensions                  │
                  └────────────────────────────────────────────────┘
                                   ▲
                                   │ MCP (Streamable HTTP / SSE)
                                   │
                  ┌────────────────┴────────────────┐
                  │   Agent  (Claude Code, Cursor,  │
                  │   Codex, Nanite, …)             │
                  └─────────────────────────────────┘
```

Both the agent (over MCP) and the browser (over WS) talk to the same
binary. They meet in the **room** — a per-session piece of state created
when the agent invokes a workflow tool. The agent puts an envelope in;
the user resolves it through the SPA; the response travels back through
the room to the agent's MCP call.

## Layers

### HTTP server with embedded SPA

`internal/server/` — a single `net/http` server on port `7842`
(`TANGENT_HTTP_PORT` overrides). The Vite production build is embedded
into the Go binary via a `//go:embed all:ui_dist` directive inside the
`internal/server` package (the path is relative to that package, and
the `all:` prefix is required so dotfiles like `.gitkeep` get included
in the embedded FS). One `tangent` executable serves both API and
frontend. In dev mode
(`make dev`) the server proxies non-API routes to Vite at `:5173` for
HMR. Rooms are exposed at `/r/<roomID>` and fall through to the SPA,
which uses React Router to pick up the ID and connect over WS.

### MCP server (Streamable HTTP + SSE)

`internal/mcp/` — built on the official MCP Go SDK
(`github.com/modelcontextprotocol/go-sdk`). Mounts `/mcp` (Streamable
HTTP) and `/sse` (legacy SSE) on the same port as the SPA. Runs in
**stateless + JSONResponse** mode for v0.1: one-shot `tools/list` and
`tools/call` calls succeed without a prior `initialize`, which keeps
the curl smoke probes simple and matches what Claude Code's HTTP
transport actually does. Stateful behaviour returns when a session-bound
workflow needs it.

Nine tools are advertised in v0.2:

- `tangent.list_workflows` — discovery.
- `tangent.triage` — the bundled triage workflow.
- `tangent.feedback` — the bundled structured-form workflow.
- `tangent.design-iteration` — sandboxed HTML preview + click/input iteration.
- `tangent.session_create`
- `tangent.session_advance`
- `tangent.session_get`
- `tangent.session_close`
- `tangent.session_list`

### WebSocket bridge with per-room state

`internal/server/ws_*.go` — the SPA opens a WS connection scoped to the
room ID; the bridge sends the active envelope, receives the user's
response, and resolves the waiting MCP call. Rooms are independent, so
multiple agent sessions can have active Tangent windows concurrently
without cross-talk.

### Persistence layer

`internal/db/` + `internal/room/` — Tangent persists room rows and
resolved envelope history in SQLite at `~/.tangent/tangent.db`
(`TANGENT_DB_PATH` overrides). Schema changes are managed with embedded
`golang-migrate` migrations. On startup Tangent opens the DB, applies
migrations, hydrates prior room state into the manager, and keeps active
in-memory `Pending` state only for envelopes that are currently awaiting
a browser response.

### Multi-envelope rooms

v0.2 turns a room from a one-shot handoff into a longer-lived session.
One room can receive many envelopes across its lifetime, and the agent
contract for that is the `tangent.session_*` MCP surface. Bundled tools
like `tangent.triage` still work, but now route internally through
session creation plus session advance. Room reuse is driven by the
`roomID` meta field on envelopes and by explicit room IDs in the session
tools.

### Envelope dispatcher

`internal/envelope/` — the bridge between MCP tool calls and the
envelope-rendering loop. Validates the incoming envelope against the
registered schemas, routes it to the right handler, and packages the
agent's reply as an envelope response. Handlers register themselves at
startup (see `internal/envelope/extensions/triage.go` for the v0.1
precedent).

### go-envelopes registry

`github.com/hollis-labs/go-envelopes` v0.1.0 — the Go side of the
shared envelope catalog. Tangent loads it at startup and prints a
`loaded envelope types count=26` line on boot. Tangent extends the
catalog with `tangent.triage` via the plugin extension API rather than
forking the registry. The `ui/src/generated/envelope-types.ts` file is
codegen'd from this catalog (`make generate-envelopes`); CI gates on
staleness via `make check-envelopes`.

## Wails note

Today Tangent is a Go HTTP server + embedded Vite SPA, not a Wails
desktop app. Wails wrapping is a future migration: the embedded-SPA
shape is deliberately chosen so the eventual Wails wrap is mechanical
— the same Go server can run inside a Wails shell, and the same SPA
build is what the shell loads. v0.1 ships as the localhost binary so
the shape can be proven before a desktop wrapper is added.

## Limits (v0.2)

- **Localhost only.** No remote access, no auth, no capability gating.
- **Single-user.** Multiple concurrent agent sessions are supported
  (multi-room), but they share one machine, one process, one user.
- **One active pending envelope per room.** History persists, but only one
  envelope at a time can be awaiting submission in a given room.
- **Two transports, one envelope schema.** MCP today; the
  Nanite-native side-channel (mid-turn event injection) is v0.5+.

## Sandboxing for design-iteration

`tangent.design-iteration` renders agent-authored HTML in an iframe
using `srcdoc`. That HTML is untrusted display content, so Tangent
keeps the sandbox deliberately narrow:

- `sandbox="allow-scripts"` only.
- No `allow-same-origin`, `allow-forms`, `allow-popups`,
  `allow-top-navigation`, or `allow-modals`.
- A CSP inside the `srcdoc` blocks network (`connect-src 'none'`),
  nested frames, workers, objects, forms, and base-uri changes.

The only active code Tangent permits is one injected inline shim that
binds click-region selectors and forwards the selected action to the
parent window via `postMessage`. This is why `allow-scripts` is present
at all: without it, the iteration loop cannot return in-iframe click
events to the agent. The lack of `allow-same-origin` is intentional;
the parent never reaches into the iframe DOM directly, and the iframe
does not get ambient access to the app origin.

## Multi-room concurrency + tab strip

v0.2 turns rooms into a persistent multi-room substrate rather than a
single ephemeral handoff. The key pieces are:

- per-room state in `internal/room`, keyed by room ID
- room history persisted in SQLite and surfaced through
  `tangent.session_get`
- room listing surfaced through `tangent.session_list`
- a browser tab strip that polls the room list and lets the user switch
  between `/r/<roomID>` routes without losing the shared SPA shell

Each room still owns exactly one active WebSocket attachment at a time.
Switching rooms in the SPA closes the current socket and reattaches to
the next room. If the browser tab is closed mid-envelope, the SPA makes
a best-effort cancel during `beforeunload`; browsers do not guarantee
that async work completes there, so the hook is a UX improvement rather
than a hard delivery guarantee. The server-side room timeout/cancel path
remains the correctness backstop for abandoned envelopes.
