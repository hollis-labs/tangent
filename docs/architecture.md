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

The production build advertises 40 tools. Its compatibility surface contains
these 25 room/workflow and session tools:

- `tangent.list_workflows` — discovery.
- `tangent.triage` — the bundled triage workflow.
- `tangent.feedback` — the bundled structured-form workflow.
- `tangent.form-collect` — the bundled generalized schema-driven form workflow.
- `tangent.design-iteration` — sandboxed HTML preview + click/input iteration.
- `tangent.whiteboard` — room-backed freeform canvas with explicit submit.
- `tangent.spreadsheet-review` — room-backed dense table review with explicit submit.
- `tangent.dashboard` — room-backed workflow-state dashboard with explicit refresh/update submit.
- `tangent.diff-review` — room-backed diff review with explicit submit.
- `tangent.file-picker` — room-backed file selection with explicit submit.
- `tangent.progress-panel` — room-backed progress tracking with explicit submit.
- `tangent.wizard` — room-backed step wizard with partial updates, navigation, and explicit final completion.
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

The generic durable substrate adds 11 handle-based tools:

- `tangent.interaction_list_kinds`
- `tangent.interaction_resolve_definition`
- `tangent.surface_open`
- `tangent.surface_get`
- `tangent.surface_close`
- `tangent.interaction_submit`
- `tangent.interaction_get`
- `tangent.interaction_await`
- `tangent.interaction_cancel`
- `tangent.interaction_supersede`
- `tangent.interaction_acknowledge`

Every tool in the first list except `tangent.list_workflows` and the phase,
close, create, get, and list session operations is room-backed, and each of
those accepts the shared optional `completion` selector described next.

### Room workflow completion

`internal/roomflow/` — every named room workflow and `tangent.session_advance`
routes through one shared compatibility adapter onto the canonical durable
substrate. The adapter separates three facts the v0.12 blocking path conflated:
that the request exists, that the human answered, and that the caller is still
listening.

A durable interaction is created before any wait can lose it, keyed by caller
scope + workflow kind + envelope id. Wait mode preserves the exact v0.12
response for interactions answered within 45 seconds; past that it returns a
successful pending receipt carrying the durable handle and room URL, never an
error and never a cancellation. `completion.mode: "async"` returns that receipt
immediately. Callers recover through `tangent.interaction_get`,
`tangent.interaction_await`, or by retrying the identical original invocation.

Transport loss, caller timeout, browser disconnect, and process restart stop
only the active waiter. Only a participant submission, a participant
cancellation, an authorized caller cancellation, or an authorized room close
produces a terminal outcome. Room presentation and history are rebuilt from
canonical records at startup; the in-memory pending map and the v0.12 `rooms` /
`envelopes` tables are compatibility projections, never a terminal-state
authority. See `docs/room-workflow-completion.md`.

The reserved operator inbox adds four stricter adapters:

- `tangent.hitl_enqueue`
- `tangent.hitl_get`
- `tangent.hitl_await`
- `tangent.hitl_withdraw`

### WebSocket bridge with per-room state

`internal/ws` — the SPA opens a WS connection scoped to the room ID; the bridge
presents active envelopes with a monotonically increasing presentation
revision, receives the user's revision-pinned response, and resolves the
waiting MCP call. A socket is a replaceable attachment: pending work is
replayed at a fresh revision after refresh or reconnect, while replaced or
stale presentations cannot resolve it. Rooms are independent, so multiple agent
sessions can have active Tangent windows concurrently without cross-talk.

### Connection lifecycle: multiple clients per surface

`internal/room/connection.go` implements the Connection lifecycle of
[ADR 0001](adr/0001-lifecycle-boundaries.md). A room tracks a *set* of live
connections rather than one socket, each with a server-issued `connection_id`:

- **Roles.** Exactly one connection at a time holds the surface's **resolver
  lease** and may turn a submission into a terminal outcome. Every other
  connection is an **observer**: it receives every presentation and changes
  nothing. Two tabs can therefore watch one surface without either closing it
  or stealing the other's work.
- **Refresh versus a second tab.** A client supplies a `clientID` — the
  identity of the tab, held in `sessionStorage`, which is per-tab and survives
  a reload. Reattaching under a `clientID` that is already present replaces
  that predecessor and inherits its lease (a refresh); a different `clientID`
  joins alongside (a second tab). This is the one-active-connection
  compatibility policy ADR 0001 permits, narrowed to the one case where a
  second live socket would be a duplicate rather than a peer.
- **Arbitration.** The lease carries an owner and an expiry renewed by the
  holder's own inbound traffic (`ResolverLeaseTTL`, 30s; the SPA heartbeats
  every 10s). A closed socket releases it immediately; the expiry only covers
  a tab that is frozen while its TCP connection still looks alive. A submission
  from a non-holder is refused with an explicit
  `{"type":"error","code":"resolver_lease_held"}` frame naming the holder, and
  an explicit `claim_resolver` takeover is the documented way out.
- **Two independent checks.** The lease answers "may this client act at all";
  the presentation revision still answers "is this client acting on the frame
  it was shown". Revisions are per connection, so a stale view is refused with
  `code:"stale_presentation"` and immediately re-presented.
- **Durable synchronization.** On attach — and on a client-requested `resync` —
  the server sends a `sync` frame built by reading the canonical interaction and
  surface revisions behind each live presentation, not the room's in-memory
  state. A client therefore knows which durable revisions its view corresponds
  to.
- **Visible independently.** Connection state is its own wire frame
  (`{"type":"connection",...}`), its own SPA component
  (`ui/src/components/ConnectionStatus.tsx`), a `connections` block on
  `tangent.session_get`, and `connection_count` / `resolver_lease` on
  `tangent.session_list`. None of it is folded into interaction status.

Every connection carries a `ParticipantBinding`, established by the ws
handler's `ParticipantResolver` before the upgrade is accepted. Today that is
an explicitly unverified loopback operator; it is the seam
[ADR 0004](adr/0004-caller-participant-and-room-access-authority.md) replaces
with a participant session, including the ability to refuse the upgrade. A
connection holds no capability and grants none.

### Durable HITL operator surface

`internal/hitl/` owns the persistent operator inbox at `/hitl`, independently
of room and browser lifecycles. The SPA reads a dedicated localhost API under
`/api/hitl`: one snapshot supplies the global pending FIFO and a separate
terminal history, item deep links inspect without recording caller retrievals,
and revision-pinned present/resolve commands commit exactly one item. The
browser adapter exposes the durable presented-projection token outside the
strict caller contract so refresh and restart can safely resume a decision.
The API rejects non-loopback hosts and cross-origin browser requests, including
same-origin DNS-rebinding attempts; command bodies must be JSON.

Inbox refreshes use one SQLite read transaction containing only the surface,
interactions, and terminal resolutions. Queue positions are derived from that
same immutable snapshot, avoiding mixed-revision projections without loading
delivery and audit journals that the operator view does not render.

`GET /api/hitl/events` carries revision hints only. Each hint causes the SPA to
reload durable state; an event is never treated as the state itself. Lost or
replaced connections therefore cannot resolve, reorder, or discard work, and a
stale tab receives an explicit conflict instead of overwriting the winning
terminal outcome. A failed durable refresh closes the stream so the browser
reports a degraded connection and retries. This channel is intentionally
separate from both per-room `/ws` and the four caller-facing
`tangent.hitl_*` MCP tools.

### Persistence layer

`internal/db/` + `internal/room/` — Tangent persists room rows and
resolved envelope history in SQLite at `~/.tangent/tangent.db`
(`TANGENT_DB_PATH` overrides). Schema changes are managed with embedded
`golang-migrate` migrations. On startup Tangent opens the DB, applies
migrations, hydrates prior room state into the manager, and keeps active
in-memory `Pending` state only for envelopes that are currently awaiting
a browser response.

Canonical interactions recover independently from those compatibility room
waiters. Startup preserves staged, presented, draft-bearing, resolved, and
delivered-but-unacknowledged records. An abandoned delivery lease is sealed as
an outcome-unknown attempt: caller-pull and explicitly idempotent destinations
can be claimed again with the same durable idempotency key, while destinations
without a safe replay policy remain paused for reconciliation. Legacy pending
envelopes with no canonical interaction retain the predictable
`SERVER_RESTART` timeout projection.

A delivery claim commits its lease, open attempt record, and `delivery.started`
event before an adapter can perform destination I/O. Completion or restart
seals that attempt exactly once and appends the corresponding outcome event in
the same transaction as the delivery revision transition.

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

The same substrate also carries dashboard room state under the
`dashboard` phase projection. `tangent.session_get` surfaces a
dedicated `dashboard` view when present:

- `dashboard_id`
- canonical `tiles`
- normalized `layout`
- room-backed `saved_layouts`
- `active_layout_id`
- normalized `query_state`
- summary fields
- append-only `snapshot_history`
- deterministic `export_state`

This keeps the dashboard workflow as a concise summary of Tangent's own
room/workflow state rather than introducing a separate reporting store
or external data sync path.

The same substrate also carries wizard room state under the `wizard`
phase projection. `tangent.session_get` surfaces a dedicated `wizard`
view when present:

- `wizard_id`
- canonical `steps`
- `current_step_id`
- normalized accepted `progress`
- deterministic `branch_selections`
- summary fields including completion status
- `updated_at`

The browser host may keep unsent step edits in local storage for draft
recovery, but the room-backed `wizard` projection remains the canonical
reopen source.

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

`github.com/hollis-labs/go-envelopes` v0.1.0 — the Go side of the shared
envelope catalog. Tangent loads 26 core definitions, then registers its own
extensions—including the non-renderer `tangent.hitl-item` interaction
definition—for 44 definitions in the shipped process. Extensions use the
plugin API rather than forking the registry.

`ui/src/generated/envelope-types.ts` is generated from all 44 shipped
definitions (`make generate-envelopes`) and CI gates it with
`make check-envelopes`. The dump tool builds its registry through the same
`extensions.RegisterAll` the server calls, so the staleness gate watches the
kinds Tangent actually renders (ADR 0003 §9 S1). Tangent-owned workflow
schemas remain alongside their extension registrations; the generated types
are a typed mirror of them, not a replacement for the hand-written component
props. The strict HITL request/result bundle is
`internal/envelope/extensions/hitl_item_schema.json`; the dedicated `/hitl`
client types live with `ui/src/lib/hitl-api.ts` and the evidence component.

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

A room accepts several simultaneous attachments, in the roles described under
"Connection lifecycle" above. Switching rooms in the SPA closes only that tab's
own socket and reattaches to the next room; any other tab attached to the room
being left keeps its connection and its ability to answer. Switching routes,
refreshing, closing the browser, or losing transport changes connection state
only; it does not cancel or close pending work. Reopening a room replays its
active envelope at a fresh presentation revision. Explicit workflow
cancellation and explicit surface/room close remain terminal operations.
