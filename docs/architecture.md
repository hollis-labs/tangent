# Tangent architecture (v0.1)

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

Two tools advertised today:

- `tangent.list_workflows` — discovery.
- `tangent.triage` — the bundled triage workflow.
- `tangent.feedback` — the bundled structured-form workflow.
- `tangent.design-iteration` — sandboxed HTML preview + click/input iteration.

### WebSocket bridge with per-room state

`internal/server/ws_*.go` — every MCP workflow call creates a fresh
room. The SPA opens a WS connection scoped to the room ID; the bridge
sends the envelope, receives the user's response, and resolves the
MCP call. Rooms are independent — multiple agent sessions can have
active Tangent windows concurrently without cross-talk. State is
in-memory and ephemeral; it does not survive a server restart.

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

## Limits (v0.1)

- **Localhost only.** No remote access, no auth, no capability gating.
- **Single-user.** Multiple concurrent agent sessions are supported
  (multi-room), but they share one machine, one process, one user.
- **Ephemeral state.** Rooms and envelopes live in memory; nothing
  persists across restarts.
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

## Multi-room concurrency

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
