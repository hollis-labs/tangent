# Tangent architecture (v0.6 form-collect release)

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

Twenty tools are advertised in the current build:

- `tangent.list_workflows` — discovery.
- `tangent.triage` — the bundled triage workflow.
- `tangent.feedback` — the bundled structured-form workflow.
- `tangent.form-collect` — the bundled generalized schema-driven form workflow.
- `tangent.design-iteration` — sandboxed HTML preview + click/input iteration.
- `tangent.whiteboard` — room-backed freeform canvas with explicit submit.
- `tangent.spreadsheet-review` — room-backed dense table review with explicit submit.
- `tangent.approval-queue` — room-backed serialized approval review with explicit submit.
- `tangent.interview_question` — one long-form question/answer turn inside a room.
- `tangent.block_draft` — drafting-stage block review and accept/revise capture.
- `tangent.prose_revision` — explicit per-suggestion review/copy/style outcomes.
- `tangent.output_render` — final markdown renderer with copy/export affordances.
- `tangent.synthesis_notes` — phase-gated synthesis handoff.
- `tangent.session_create`
- `tangent.session_advance`
- `tangent.session_get`
- `tangent.session_advance_phase`
- `tangent.session_set_phase_output`
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

Rooms also carry first-class workflow phase state:

- `current_phase`: the room's current workflow phase ID.
- `phases_visited`: append-only ordered phase history.
- `phase_outputs`: a map of phase ID to versioned JSON blob, currently
  `{"version":1,"data":{...}}`.

This substrate is intentionally workflow-neutral. Built-in or external
agents can move a room through arbitrary phase IDs without registering a
global sequence, and phase outputs can be rehydrated cheaply through
`tangent.session_get` after process restart.

The same substrate now also carries form-collect room state under the
`form-collect` phase projection. `tangent.session_get` surfaces a
dedicated `form_collect` view when present:

- `form_id`
- `intent`
- canonical `schema`
- normalized `answers`
- `notes`
- `updated_at`
- room-backed `saved_drafts`
- room-backed `templates`
- available `actions`
- lightweight `attachment_refs`
- durable `submission_summary`

The same substrate also carries spreadsheet-review room state under
the `spreadsheet-review` phase projection. `tangent.session_get`
surfaces a dedicated `spreadsheet_review` view when present:

- `table_id`
- canonical `columns`
- canonical `rows`
- normalized `query_state`
- `notes`
- `updated_at`
- room-backed `saved_views`
- `selected_row_ids`
- normalized `selected_rows`
- optional bulk `action_id`
- lightweight CSV `export_refs`

This keeps the table-review workflow on the same persistence path as
other room-backed workflows instead of introducing a parallel store just
for tabular review.

The shipped v0.4 whiteboard state is persisted on the same room
state path rather than in a separate table. `tangent.session_get`
projects a dedicated `whiteboard` payload when present:

- `board_id`
- `scene_snapshot` (canonical tldraw-style scene JSON)
- `assets` (references/metadata only, not inline base64 blobs)
- `export_refs` (latest export metadata, PNG-only in v0.4)
- `notes`
- `updated_at`
- `revision_history` (append-only metadata, not full duplicated scenes)

Full historical whiteboard snapshots are also retained on the room for
the in-room revision browser, but `session_get` intentionally exposes
only lightweight revision metadata so agent-side checkpoint reads do not
need to load every scene blob eagerly.

This keeps restart hydration and room replay simple while still
supporting reopen/continue-from-revision inside the room UI.

The same substrate also carries approval-queue room state under
the `approval-queue` phase projection. `tangent.session_get`
surfaces a dedicated `approval_queue` view when present:

- `queue_id`
- canonical `items`
- `current_index`
- normalized `decisions`
- queue `notes`
- `updated_at`
- append-only `audit_trail`
- lightweight audit `export_refs`

The bundled v0.3 writing flow uses the canonical sequence:

- `interview`
- `synthesis`
- `drafting`
- `revision`
- `output`

Jump-backs are explicit rather than implicit. If a revision pass uncovers a
drafting problem, the agent calls `tangent.session_advance_phase` back to
`drafting`, resolves the new block, and advances forward again. History remains
append-only in `phases_visited`.

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

## Limits (v0.5)

- **Localhost only.** No remote access, no auth, no capability gating.
- **Single-user.** Multiple concurrent agent sessions are supported
  (multi-room), but they share one machine, one process, one user.
- **One active pending envelope per room.** History persists, but only one
  envelope at a time can be awaiting submission in a given room.
- **Whiteboard is single-user localhost first.** The board is shared
  between one user and one agent through one persistent room, but there
  is no live multiplayer presence or conflict resolution yet.
- **Spreadsheet review is review-only, not a spreadsheet editor.**
  Agent-provided rows are canonical; there are no formulas, workbook
  semantics, arbitrary cell editing, or remote spreadsheet connectors.
- **Two transports, one envelope schema.** MCP today; the
  Nanite-native side-channel (mid-turn event injection) is v0.6+.

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
- room phase state persisted in the same room row and updated through
  `tangent.session_advance_phase` and
  `tangent.session_set_phase_output`
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
