# Tangent architecture

Stamped against `ce4aca8`. A claim in this document that is not true of that
commit is a defect; the **Current limitations** section at the end says what
is declared or intended rather than shipped.

A one-pager. For the user-facing setup recipe, see
[`mcp-integration.md`](./mcp-integration.md). For contributor onboarding,
see [`developing.md`](./developing.md).

## Shape

Transport connections, browser windows, Tangent surfaces, interaction
instances, agent sessions, workflow runs, and business objects are distinct
identities even when a simple deployment maps some of them one-to-one. The
sketch below is drawn along those seams rather than along the process
boundary; the boundary decision behind it is
[ADR 0005](./adr/0005-product-boundary-and-portfolio-composition.md).

```text
Envelope publisher                   Calling application
schema · response schema             Nanite · Torque · Hadron · CLI · peer app
renderer · capabilities                         |
retention defaults                              | InteractionRequest
        |                                        v
        | definition reference       +-----------------------------+
        +---------------------------> |           Tangent           |
                                      |                             |
 Portable MCP ----------------------> | transport adapters          |
 Nanite native channel ------------> | interaction application svc |
 local HTTP/API --------------------> | definition resolver         |
                                      | durable surface/room store  |
                                      | resolution + delivery log   |
                                      | extension/runtime boundary  |
                                      +--------------+--------------+
                                                     |
                                             SurfaceProjection
                                                     |
                                      +--------------v--------------+
                                      | browser or desktop shell     |
                                      | trusted renderer host        |
                                      | sandboxed extension frames   |
                                      +--------------+--------------+
                                                     |
                                             authenticated input
                                                     |
                                                     v
                                                Participant

 ResolutionRecord -------------------------------> calling application
 DeliveryReceipt <------------------------------- calling application

 Tesseract receives only explicit promoted knowledge or pointers.
 Cerberus/OS may install and supervise Tangent but do not own interactions.
```

Two of those rows are intended rather than shipped: there is no Nanite native
channel and no desktop shell in the tree today, and Tesseract is not composed
at all. Everything else runs in one binary — the agent (over MCP) and the
browser (over WS) talk to the same process. They meet in the **room**, the
compatibility projection of a surface, created when the agent invokes a
workflow tool. The agent puts an envelope in; the participant resolves it
through the SPA; the response travels back as a sealed resolution.

### The shared application service

MCP, HTTP, the WebSocket projection, CLI administration, and any future
native or desktop transport **adapt one application service. They do not
implement parallel business rules.** This is the invariant that makes a new
transport cheap and a second rule set a defect.

Every interface preserves:

- caller and participant identity
- definition and instance revisions
- idempotency
- lifecycle semantics
- typed errors
- sensitivity and authorization
- causation, correlation, and trace context

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

`internal/mcp/` — built on `github.com/hollis-labs/libs/plugin-mcp/go-mcp`, the portfolio's
shared MCP library, itself a thin wrapper around the official MCP Go SDK
(`github.com/modelcontextprotocol/go-sdk`). Mounts `/mcp` (Streamable
HTTP) and `/sse` (legacy SSE) on the same port as the SPA. Runs in
**stateless + JSONResponse** mode: one-shot `tools/list` and
`tools/call` calls succeed without a prior `initialize`, which keeps
the curl smoke probes simple and matches what Claude Code's HTTP
transport actually does. Stateful behaviour returns when a session-bound
workflow needs it. Both transports clear the server-wide read *and* write
deadlines (`longLivedMCPHandler`), which is what keeps a legacy `/sse`
subscription from being torn down mid-session by the 30s `ReadTimeout`.

The tool surface is **derived, never written down**. It has been recorded
incorrectly in this repository more times than correctly, so no count appears
in this document: `make smoke` asks the shipped binary and prints the surface
with a digest, and `internal/smoke/docs_test.go` fails when the names listed
below no longer match what the binary advertises.

The compatibility surface contains these room/workflow and session tools:

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

The generic durable substrate adds these handle-based tools:

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

The definition registry adds three payload-bounded diagnostics:

- `tangent.definition_registry_list`
- `tangent.definition_get`
- `tangent.definition_registry_diagnostics`

And operability adds three read-only probes:

- `tangent.health_report`
- `tangent.telemetry_query`
- `tangent.retention_status`

Every tool in the first list except `tangent.list_workflows` and the phase,
close, create, get, and list session operations is room-backed, and each of
those accepts the shared optional `completion` selector described next.

### Room workflow completion

`internal/roomflow/` — every named room workflow and `tangent.session_advance`
routes through one shared compatibility adapter onto the canonical durable
substrate. The adapter separates three facts the pre-foundation blocking path conflated:
that the request exists, that the human answered, and that the caller is still
listening.

A durable interaction is created before any wait can lose it, keyed by caller
scope + workflow kind + envelope id. Wait mode preserves the exact pre-foundation
response for interactions answered within 45 seconds; past that it returns a
successful pending receipt carrying the durable handle and room URL, never an
error and never a cancellation. `completion.mode: "async"` returns that receipt
immediately. Callers recover through `tangent.interaction_get`,
`tangent.interaction_await`, or by retrying the identical original invocation.

Transport loss, caller timeout, browser disconnect, and process restart stop
only the active waiter. Only a participant submission, a participant
cancellation, an authorized caller cancellation, or an authorized room close
produces a terminal outcome. Room presentation and history are rebuilt from
canonical records at startup; the in-memory pending map and the legacy `rooms` /
`envelopes` tables are compatibility projections, never a terminal-state
authority. See `docs/room-workflow-completion.md`.

The reserved operator inbox adds four stricter adapters:

- `tangent.hitl_enqueue`
- `tangent.hitl_get`
- `tangent.hitl_await`
- `tangent.hitl_withdraw`

The Docs inbox adds one caller-facing adapter, `tangent.docs_enqueue` — see
"Durable Docs operator surface" below.

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

`internal/hitl/` owns the approval and attention interaction semantics, independently
of room and browser lifecycles. These items appear in the unified Inbox. The SPA reads a dedicated localhost API under
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

### Durable Docs operator surface

`internal/docs/` owns document review semantics within the unified Inbox
(CW-20260917-0009). It sits on the same interaction substrate as HITL and
Turns — its own surface (`surface_docs_default`) and kind
(`tangent.doc-item`), not a new table — but answers a different need: a
document an agent sends the operator to read at their own pace, not a
decision that blocks anything. A caller enqueues one with
`tangent.docs_enqueue`; the SPA reads and acts on it through `/api/docs`.

Read/unread is deliberately not the interaction's own resolved/unresolved
state — a document can be read without being acknowledged, and
`requires_ack: false` documents are never resolved at all until archived.
`docs_read_receipts` (migration `0018`) is a small side table scoped to this
package alone: presence of a row means read, and nothing marks a row present
except the operator's explicit "mark read" action — never the act of fetching
or displaying a document. A document that asks for one is acknowledged via
`POST /api/docs/items/{id}/acknowledge`, which resolves the interaction the
same way HITL resolves an approval. "Delete" is `POST
.../archive`, Tangent's usual cancel-and-hide rather than a real row
deletion — the same terminal-state permanence HITL's withdraw and Turns'
dismiss already accept, so an already-acknowledged document cannot be
archived further; it is already out of the active queue.

### Cooperative relay inbox

`internal/channel/` + `internal/relay/` back seven relay MCP tools
implementing the collaboration-surface boundary of
`docs/adr/0007-collaboration-surface-plugin-host-and-view-state.md`
(which supersedes ADR 0006):
`tangent.relay_open_channel`, `tangent.relay_attach`, `tangent.relay_detach`,
`tangent.relay_send`, `tangent.relay_receive`, `tangent.relay_ack`, and
`tangent.relay_capabilities`. Every input carries `contract_version` and
stays flat at the top level — no `allOf`/`if`/`then` on a field a model
fills — because a gateway's discovery schema has been observed to drop a
conditional branch and leave the model sending an untyped value.

A channel is a persistent collaboration context independent of any runtime
session, holding a canonical operator participant and any number of
attached agent participants; a room stays an unrelated compatibility
projection of a surface (ADR 0001 §2) and is never renamed into one.
`relay_attach` binds an agent's current runtime session to a channel it
must already exist in — creation is `relay_open_channel`'s job alone, so a
mistyped or omitted channel id fails loudly rather than silently opening a
second channel nobody is watching. `relay_send` accepts one message into an
immutable exchange journal and atomically queues its delivery; the
recipient defaults to the channel's operator only when exactly one is a
live member, and refuses rather than guessing otherwise. `relay_receive`
lists new messages since a caller-held cursor, optionally waiting up to
50000 ms, and never returns an error on timeout. `relay_ack` records that a
participant consumed a message, separately from receiving it, and refuses
a caller that is not the message's recipient. `relay_capabilities` reports
what this adapter supports, with unsupported operations named explicitly
rather than simulated.

Presence — whether a receive is open on a destination right now, and when
it was last seen — is split the same way: "open now" is in-memory and
process-local, honestly lost across a restart Tangent cannot observe
through; `last_seen_at` is durable. The operator's own send/read path has
no MCP tool here; it is a REST surface over the same two Stores, not yet
built.

`internal/relay` also defines a `Provider` interface (CW-20260906-0072)
that sits between the seven MCP handlers and the two Stores: participant
resolution, default-recipient resolution, and every `relay_*` business rule
live behind it, not in `relay_tools.go`. `CLIProvider` is the only
implementation today — a request/response provider a Claude Code session's
cooperative-loop skill calls into, with nothing addressable for Tangent to
push into between calls.

`exchange_outbox` and its `ClaimNextForDelivery`/`RecordDeliveryOutcome`
methods implement a transactional outbox for push-style delivery: every
`AcceptExchange` call writes an outbox row in the same transaction as the
exchange, but `CLIProvider` never claims one. That is a deliberate,
measured decision, not a gap — CW-20260906-0071's live proof against the
dev instance confirmed the outbox has zero callers in this build. A
pull-only provider has no addressable destination to push into between
calls, so consuming the outbox would have nothing to deliver to. Leaving it
dormant costs nothing to reverse: a future push-capable provider (a
persistent-connection transport under CW-20260907-0061, or a Nanite-hosted
plugin with its own transport) starts consuming a real backlog with no
migration, because the same `AcceptExchange` call that serves today's
`CLIProvider` already wrote its work item.

`relay_receive`'s `unacked_only` input (CW-20260906-0072) is the other half
of that same pull-only shape: a relaunched session holds no cursor in
memory, so `cursor=0` alone would re-read the destination's entire history
every check-in with no way to tell new from already-handled. `unacked_only`
answers that by excluding anything already recorded in
`exchange_reads` for the caller, regardless of cursor — `cursor=0,
unacked_only=true` is exactly the durable check-in shape a freshly launched
process needs, and response size tracks genuine backlog rather than total
history. The corollary: anything received but never acknowledged via
`relay_ack` keeps reappearing on every subsequent `unacked_only` call,
forever — a caller must ack what it actually handles, including a message
it decides needs no reply, or that message is never done being delivered.

### Channel pane

`internal/channelpane/` + `/api/channels` (CW-20260907-0017) is the
operator's own send/read path over `internal/channel` and `internal/relay`
— a REST surface, not an MCP tool, since an operator has no MCP client. It
mirrors `/api/hitl`'s shape (same participant-session guard, same
same-origin check, a revision-hint SSE stream at `/api/channels/events`
that a client always follows with a refetch, never trusting the event as
state) and is deliberately the minimal pane: a channel list with unread
and needs-input counts, one addressed chat view per channel, HITL items
raised by that channel's agent shown inline via a read-only parse of
`hitl.Service.Inbox()`'s already-exported `RequestSnapshot` (no change to
`internal/hitl`'s own contract), a browser-`localStorage` draft per
channel, and enter to send.

Every read is pure: `ListChannels` and `GetChannel` call only
`relay.Store.ListForDestination`, the new `relay.Store.ListForChannel`
(both directions of one channel, ordered by `created_at` — `sequence` is
monotonic per recipient only, so it cannot interleave an operator-sent and
an agent-sent exchange chronologically), `GetRead`, and `Presence`, never
`Receive`. Only an explicit `POST /api/channels/{id}/read` writes
(`RecordRead`), and only when a caller asks for it — never as a side
effect of a GET, which is the one property this task could not ship
without.

A sent operator message reports exactly three delivery states, matching
the three facts the pull model actually produces: `queued` (the agent's
own receive is open right now — delivery is imminent), `awaiting-peer`
(the agent is idle or gone; `last_seen_at`, if any, is the only evidence
of life), and `accepted-by-peer` (a row in `exchange_reads`). A fourth,
`failed`, does not exist: nothing in the dormant-outbox pull model ever
produces a delivery failure, so rendering one would be a state the system
can never actually enter. `recipient_binding_current` is not surfaced at
all — a reconnect and an explicit rebind are the same code path
(`channel.Store.Rebind`), so a channel's entire history reads
binding-not-current after any ordinary agent relaunch; that is provenance,
not a delivery outcome, and omitting it is the simplest way to guarantee
this pane never paints a healthy relaunch as a wall of red.

There is no create-channel action in the pane. `internal/channel` ships
only the agent-facing `tangent.relay_open_channel`; the operator's channel
list is exactly the channels an agent has already opened, and the primary
flow runs agent-first (an agent needing input opens a channel and messages
the operator, who replies here). An operator cannot start a conversation
from this pane today — a known, recorded limitation, not an oversight.

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

### Interaction definition registry

`internal/definition/` owns the **interaction definition manifest**: the one
immutable document a publisher authors per `kind@version`, specified by
[ADR 0003](adr/0003-definition-and-package-ownership.md) §2. It carries
identity, request/response/error schemas, the renderer binding and its trust
class, host-mediated effect capabilities, draft custody and sensitivity, trust
evidence, telemetry declarations, and compatibility ranges. Tangent derives
every digest and every grant; a manifest that authors one is rejected.

Each shipped kind is a package under
`internal/envelope/extensions/packages/<package-id>/<kind>/`: an authored
`manifest.yaml` plus the schema files it names, embedded as one tree. The
package id is the ADR 0003 §6 ownership assignment made mechanical, and
`internal/envelope/extensions/register_all.go` is the single table that binds
a wire name to it.

That table has one row per kind and a column saying which of **two doors** the
kind arrives through. Most are installed directly by `extensions.RegisterAll`.
A row marked `contributedByPlugin` is installed by the plugin host instead (see
[the plugin host](#plugin-host), below), and `RegisterAll` skips it — a kind
registered twice is an error, and routing one around the manifest requirement
would defeat the point of having it. Both doors appear in `RegisteredTypes()`
and `PackagedKinds()`, so `TestPackageTreeMatchesRegistrations` walks both;
if it walked only one, the ownership guarantee would silently narrow to half
the registry.

`envelope.Service` holds the **version-indexed registry** — `kind -> version ->
material` — beside the upstream go-envelopes registry, which stays the
single-current-version validator. `Service.RegisterDefinition` builds the
`TypeSpec` directly so it can populate `TypeSpec.PayloadSchema`, which no
upstream manifest path reaches; nothing in go-envelopes changes.

A submission pins a composite `binding_digest` covering the manifest and schema
bytes, the identity triple, the contract digest, the renderer binding, both
capability sets, the trust assurance, and `envelopeDefinitionValidatorRevision`.
`interaction.Service.SubmitInteraction` retains the exact material behind that
digest in `definition_manifests` (migration 0007, immutable) *before* writing
the record. Validation then resolves material by the pinned digest — from the
in-memory index only when recomputing its digest reproduces the pin, otherwise
from the retained table — and never through the current registry entry. That is
what makes a pinned interaction still validatable after a restart, after the
installed catalog changes, and after the current version moves on.

A definition the host cannot serve is a *state*, not an error to paper over:
`incompatible`, `quarantined`, and `unavailable` are distinguishable in
`tangent.definition_registry_list` and in the typed resolution failure, each
carrying a reused go-envelopes error code. A fallback renderer is used only
when the manifest declares one whose `preserves_meaning` is true.
`tangent.definition_get` and `tangent.definition_registry_diagnostics` complete
the payload-bounded diagnostics surface.

### Plugin host

`internal/pluginhost/` implements the portfolio plugin framework's `Host`
contract (`github.com/hollis-labs/libs/plugin-mcp/plugin-sdk`), and it is how a new interaction
kind can arrive without editing Tangent's own registration table.
[ADR 0007](adr/0007-collaboration-surface-plugin-host-and-view-state.md) §4 is
the boundary decision and [ADR 0008](adr/0008-the-plugin-model.md) is the model
this host is moving toward. The rule is one sentence, and it is the half that
has never moved:

> The SDK says what a plugin may offer. The ADR 0003 manifest says what the
> host will let it do. A registration without a manifest is refused.

So `RegisterUIComponent` for an envelope component does not accept a
description of a kind — it accepts a **name**. The host resolves the manifest
this build ships for that name and refuses the registration when there is none.
A plugin cannot supply manifest bytes of its own, because a manifest a plugin
authored would be a plugin deciding its own trust class. It also cannot author
a trust class, capability set, assurance or digest through `UIComponent.Props`:
those keys are refused by name rather than ignored, so a plugin that thinks it
raised its own trust class fails to load instead of being silently downgraded.

`internal/plugins/installed.go` scans the installed plugin directory and resolves
strict SDK manifest-v2 declarations and Tangent extension schema 1 into
Tangent registrations. Install and discovery both verify exact native bundle
inventories, strict SemVer identities, explicit host/engine contract ranges and
approved definition references. Loading pins a private read-only verified
snapshot, with writable data/cache outside its inventory; no legacy data moves.
The public declaration and native runner contracts are each 1.0.0, independent
of the application release. See [the plugin guide](writing-a-plugin.md#process-manifest-v2-and-the-tangent-extension)
for the format and refusal boundaries. `ChildPlugin` uses
[`plugin-host`](https://github.com/hollis-labs/libs/tree/main/plugin-mcp/plugin-host) Lifecycle for spawn,
protocol-2 handshake, process groups, bounded teardown and crash recovery; the
private child and wire implementations are removed. A host process shares one
random epoch and an in-memory generation store across its controllers. Each
attempt gets a fresh incarnation, explicit empty grants and a detached reviewed
configuration snapshot, with
no host-service or hooks-profile offers. Identity and version must match the
resolved manifest before load or registration, and frames are bounded to 8 MiB
in both directions.

Tangent explicitly classifies an unexpected exit of an activated child as
transient, preserving automatic recovery with a cumulative budget of three
restarts and cancellable 1/2/4-second backoff. Intentional stop never retries;
protocol, identity, version, Init and capability failures are terminal. A fresh
process gets a fresh on-demand health gate; an unhealthy cached verdict refuses
dispatch until a later probe succeeds. Manifest resolution, registration,
authorization and the 30-second dispatch policy remain Tangent-owned.

The first-party plugins live in
[`hollis-labs/tangent-plugins`](https://github.com/hollis-labs/tangent-plugins),
written against `pkg/plugin` and selected by `tangent-plugins.version`.
The SDK, lifecycle driver and MCP transports come from the released
`github.com/hollis-labs/libs/plugin-mcp` module at v0.1.1. Its finite forward
calls carry a protocol-2 `context` budget, so plugins must use an SDK that
accepts that field. The first-party installation pin remains a separate
pseudo-version; adopting the module does not rebuild or install those binaries.
Follow the activation prerequisites and installation order in
[the plugin guide](writing-a-plugin.md). Existing protocol-1 binaries and
protocol-2 builds whose strict decoder predates `context` must be rebuilt
before they can load.
Local MCP callbacks remain the existing plugin-to-host path; empty grants do
not claim enforcement over that local caller's authority. Duplex host RPC remains separate. Enable intent is persisted independently of
plugin configuration; registrations belong to their exact load owner.

**Two more surfaces extend the SDK's base contract** (`internal/pluginhost/mcp.go`,
`internal/pluginhost/http.go`). The SDK's `Host` carries neither, and says in as
many words that a host application may add its own; a plugin implements the
SDK's own `subprocess.MCPHandler` / `subprocess.HTTPHandler` for dispatch, so
nothing a plugin author writes is Tangent-shaped.

- **`RegisterMCPTool`** contributes an agent-callable tool, which is what lets
  one agent call reach plugin code instead of a model shaping a payload by
  hand. A plugin tool is an ordinary tool: it appears in `tools/list` on both
  transports, its arguments are validated against its own declared schema
  before dispatch, and **the documentation gate covers it** — the gate asks the
  shipped binary what it serves, so a plugin tool documented nowhere fails
  `make smoke`, deliberately. What a plugin cannot do is shadow: a name already
  claimed by a host tool or another plugin is refused by name, because the MCP
  SDK's registry is keyed by name and would otherwise have kept the plugin's
  handler silently. Names must be `tangent.<name>`, which is the spelling the
  documentation gate matches.
- **`RegisterHTTPRoute`** contributes a browser route under the reserved
  `/api/plugins/<plugin>/` prefix, so a button in a room can do work with no
  agent turn at all. It is not a side door: `internal/server/plugin_routes.go`
  mounts it through the same `registerParticipantRoute` every other browser API
  route uses, so it carries the same-origin guard, the participant-session
  requirement and the ADR 0004 §7 capability check by construction — and it
  appears in `ParticipantRoutes()`, which is what puts it inside
  `TestEveryParticipantGuardedRouteUsesACapabilityParticipantsHold`. A route
  gated on a capability the participant row never holds is refused at
  registration rather than answering 403 forever. Streaming is unsupported per
  the SDK's own contract. The participant's session cookie never crosses the
  boundary inbound (ADR 0004 §6.1 makes it capability material) and
  `Set-Cookie` never crosses it outbound; both directions are allowlisted, and
  a dropped response header is logged by name.

Both surfaces are recorded through an owner-scoped host at plugin load.
The composition root attaches the live MCP registry, and HTTP requests resolve
the current route map behind participant guards, so disable/reload changes the
served surface without restarting Tangent. Initial plugins load before the
MCP and HTTP servers exist: a plugin-contributed envelope kind has
to be in the registry `mcp.New` reads. A plugin handler that errors or panics
is contained and reported (`PLUGIN_FAILED` / `PLUGIN_PANICKED` on a tool, a
`plugin_error` refusal body on a route); one plugin's defect is not every
caller's outage.

**How a plugin drives Tangent** is one method, `plugin.ToolCaller` (`pkg/plugin`), and
that narrowness is the decision. A plugin opening a room and keeping it fresh
is doing what an agent does, so it is the same kind of caller: `mcp.LoopbackCaller`
connects an in-process MCP client session over the SDK's in-memory transport,
which means a plugin's calls go through the real tool surface, the real
middleware, the real schema validation and the same host-assigned caller
identity as any local MCP caller — no authority an agent does not already have.
A typed facade per need would grow the host one method at a time; `GetService`
stays unimplemented for the opposite reason, being untyped and unbounded. The
composition root closes the session alongside the database.

Deliberately not implemented, and each returns an error rather than `nil` so a
registration cannot silently succeed and do nothing: `RegisterCRUDHandler`
(a host surface that writes to applications on a plugin's behalf is Tangent
growing an application dependency; a plugin holding its own client is userland
choosing one — see ADR 0007 §6's 2026-09-10 amendment for where that line now
sits), `RegisterEventHook`, `GetService`,
`GetConfig`/`SetConfig`, `RegisterConfigSchema`, `RegisterConnector`,
`RegisterProvider`, `RegisterCLIAdapter`.

Subprocess plugins, runtime asset loading and signature verification are a
different kind of absence and are recorded in a different place. They are not
surfaces this host refuses — they are modes it does not run yet, and
[ADR 0008](adr/0008-the-plugin-model.md) §5 records the first two as the target
rather than as exclusions. ADR 0007 §4 listed all three as excluded, and two of
the three have since been reversed; that is why the two lists are now separate.
Signing stays out of scope, first-party only, unchanged.

**A plugin-contributed kind is not a privileged one.** It goes through the same
manifest, the same validation and the same renderer isolation as every
host-package kind. What used to be said here — that `core-trusted` stays
unreachable for a publisher that is not `tangent` or `hollis-labs/go-envelopes`
— was removed by ADR 0009 along with the class it gated; the equal treatment it
was asserting survives it.

### Long-lived surfaces and `tangent-custodied` view state

Some surfaces stay open while the participant works in them rather than
settling in one act. `tangent.session_advance` with `completion: async` returns
a durable pending receipt and cancels nothing, so an interaction can stay
pending indefinitely; what was missing was a way to record anything from such a
surface short of resolving it.

A participant's non-terminal state — which filters are applied, which record is
selected, whether a detail pane is open — is recorded as a **draft revision**
under host custody (ADR 0007 §5). The path is
`ws-client.saveDraft` → a `draft` frame → `Room.HandleDraftFrom` →
`roomDisposition.Draft` → `interaction.Service.SaveDraft`, and the caller reads
it back by pulling `tangent.surface_get`, which projects `Drafts[]`.

Three properties hold it in place:

- **A draft never takes the resolver lease.** `HandleDraftFrom` deliberately
  bypasses `authorizeDisposition`, because a response and a cancel are
  competitions — exactly one connection may settle an envelope — and a draft is
  not. If it took the lease, opening a board in a second tab would silently
  steal the right to answer from the first.
- **A stale revision is refused, never merged.** The store computes
  `MAX(revision) + 1` and rejects anything else; `ws-client` owns the counter
  per envelope so a caller cannot supply one.
- **Reading view state is a pull.** Tangent does not push a participant's
  in-progress state at a caller, and a caller must not present a draft as a
  decision. A draft is what the user is looking at; only a resolution is what
  they decided.

`tangent.app-board` is the first kind whose manifest declares
`draft_custody: tangent-custodied` — a value the manifest format has carried
since `2e2c48a` and that had been waiting for a kind that meant it. Every kind
shipped before it says `browser-local`, truthfully describing a
`ui/src/lib/*-draft-storage.ts` module backed by `localStorage`.

**A staged change is view state too, and that is a boundary rather than a
convenience.** When a board's caller supplies a `sync` block, the participant
can move a card into another column; the move is recorded in the draft, the
card renders where they put it with a marker, and **nothing has happened to
the caller's records**. Pressing the board's Sync button is what applies it,
and that press is an explicit act with its own route and its own capability
check. So a board abandoned with staged changes changes nothing anywhere —
which is what keeps "a caller must not present a draft as a decision" a
property rather than a slogan. The draft is where intent accumulated; the press
is the decision.

### The app-plugin composition pattern

`tangent.torque_open_board` (the Torque plugin, `torque/` in tangent-plugins) is ADR 0007 §6's
pattern working end to end, and the shape is worth stating because it is meant
to generalize:

1. **A domain-free kind the host ships.** `tangent.app-board` describes a board
   of filtered cards with a detail pane. Torque supplies content to it; it is
   not a Torque type.
2. **The mapping lives in the plugin.** A Torque status becomes a column, a tag
   becomes a badge, a description becomes the card body. All of it is a `for`
   loop, which is the point — a model asked to shape this payload would be
   doing mechanical work expensively and occasionally wrong.
3. **One agent call.** The agent passes filters and receives a room URL. It
   never shapes a payload, and it is the caller: an agent running anywhere,
   including inside Torque, drives Tangent, while Torque stays an engine that
   is called and returns.
4. **A sync that costs no agent turn.** The board's Sync button reaches the
   plugin's own HTTP route, which applies the staged changes through Torque's
   API, re-queries with the board's originating filters, and replaces the
   board. Refreshing a long-lived board means withdrawing the pending
   interaction and advancing a fresh one onto the same room — the withdrawal
   retires the room's presentation, which is what `Room.Release` always
   documented itself for and which nothing called until `CW-20260910-0031`.

The costs are recorded rather than absorbed. Compiled in, the plugin's Torque
writes originate in Tangent's process; ADR 0007 §6's 2026-09-10 amendment says
so explicitly and names `CW-20260910-0034` (subprocess mode) as the fix. What
the amendment does not relax: Tangent core still holds no application
dependency, `RegisterCRUDHandler` is still refused, and the plugin still
reaches Tangent only through the tool surface an agent uses.

#### What the second plugin found

`tangent.tesseract_review` (the Tesseract plugin, `tesseract/` in tangent-plugins) is the same five
steps against a different application, which is what it was built to test
(`CW-20260910-0054`). Steps 1 through 3 held unchanged: a domain-free kind, a
mechanical mapping in userland, one agent call in. Step 4 did not.

The pattern's word for what a sync applies was **mechanical** — a status
transition, a `for` loop, the kind of work a model would do expensively and
occasionally wrong. That word turned out to describe a property of *Torque*
rather than of application plugins. Tesseract's memory revisions are immutable
except for deprecation, and the memory domain exposes no status route, so
promoting a record up its lifecycle is a new revision carrying the whole payload
forward with `supersedes` — an authored write, and the same act as rewording it.

So the pattern generalizes with the boundary stated by consequence rather than
by surface:

> A plugin applies what is mechanical **in the owning application's own terms**,
> and hands back what that application makes an authored act. Which side a
> disposition falls on is the application's answer, not the plugin's.

For this plugin that leaves exactly one write — a deprecation — and everything
else travels back as a work list on the sync's response, durable in the board's
draft, with the card wearing a badge until the request clears itself. Keeping
the write surface one call wide is also the cheapest way to keep an
already-recorded limitation honest: Tangent holds no Tesseract credential and
issues no identity, so a write a plugin makes is authorized by the owning
store's policy and by nothing this host vouched for (`CW-20260910-0045`).

The board also needed something `tangent.app-board` did not have — a free-text
control per card, since a board could say where a card should *go* but not
anything *about* it. That went into the app-board package as `sync.note_label`
plus a `staged_notes` map in the draft (manifest revision 3, additive), not into
the Tesseract plugin: a note is domain-free, and the kind neither interprets one
nor sends it anywhere. That it belongs to the host rather than to either
consumer is the whole reason a second application could ask for it.

#### The scaffold the third plugin starts from

Two plugins is enough to tell what generalizes from what one application
happened to need, so the pattern is now extracted into a scaffold rather than
re-derived (`CW-20260910-0035`). `go run ./cmd/tangent-new-plugin -package
<name>` writes a plugin that loads; `internal/plugintemplate/` holds the
templates and two committed renders, and
[`writing-a-plugin.md`](./writing-a-plugin.md) carries the reasoning — including
which of the eight measured differences between the two plugins is essential,
which was incidental to its application, and which is a trap the scaffold
prevents.

The two presets never mix. One fills an existing domain-free kind and knows
about one application; the other contributes a kind and knows about none. A
plugin that did both would be a domain-free kind with one application's concepts
in it, which is how the boundary above rots — so the generator refuses the
combination rather than trusting a reviewer to catch it.

### go-envelopes registry

`github.com/hollis-labs/libs/ui-go/envelopes` (module `ui-go/v0.1.0`) — the Go side of the shared
envelope catalog. Tangent loads the go-envelopes core definitions, then
registers its own extensions—including the non-renderer `tangent.hitl-item`
interaction definition. Extensions use the plugin API rather than forking the
registry. Ask `tangent.definition_registry_list` how many there are; the number
is not written down here, for the same reason the tool count is not.

`ui/src/generated/envelope-types.ts` and
`ui/src/generated/renderer-bindings.ts` are generated from every shipped
definition (`make generate-envelopes`) and CI gates them with
`make check-envelopes`. The dump tool builds its registry through the same two
doors the server uses — `extensions.RegisterAll` and then
`plugins.LoadShipped` — so the staleness gate watches the kinds Tangent
actually renders (ADR 0003 §9 S1). A generator that walked only the first door
would silently omit every plugin-contributed kind while the server served it. Tangent-owned workflow
schemas remain alongside their extension registrations; the generated types
are a typed mirror of them, not a replacement for the hand-written component
props.

Every generated artifact carries a `// @definition-source sha256:<hex>` stamp
over the ordered `(kind, version, revision, manifest_digest)` set (ADR 0003
§4). `make check-envelopes` compares that stamp first and reports *which kind*
drifted, then compares bytes to catch a hand-edit; a Go test asserts the same
stamp on every `go test ./...`. `tangent.definition_registry_list` reports the
live digest, so a client can refuse to submit against a definition it was not
generated for. `ui/src/lib/hitl-api.ts` is hand-written over the HITL `$defs`
bundle and carries ADR 0003 §4.7’s accepted floor — a `@definition-source`
stamp plus a drift test asserting it still matches the bundle.

The strict HITL request/result bundle is
`internal/envelope/extensions/packages/tangent.hitl/hitl-item/request.schema.json`,
and its response and error schemas are drift-tested projections of the same
`$defs`. The dedicated `/inbox` client types live with `ui/src/lib/hitl-api.ts`
and the evidence component.

## Wails note (future, not shipped)

Today Tangent is a Go HTTP server + embedded Vite SPA served to an ordinary
browser. **There is no Wails dependency, no desktop shell, and no system tray
in the tree.** Wails wrapping is a future migration and has not been started:
the embedded-SPA shape is deliberately chosen so the eventual wrap stays
mechanical — the same Go server can run inside a Wails shell, and the same SPA
build is what the shell would load. Tangent ships as the localhost binary so
the shape can be proven before a desktop wrapper is added. The same applies to
the Nanite-native side channel drawn in the sketch above: intended direction,
no implementation, no committed release.

## Room access and caller scope

Implements [ADR 0004](adr/0004-caller-participant-and-room-access-authority.md).

**A room URL is a locator, not a credential.** `/r/{roomID}`, the `/inbox`
inbox, and item deep links may appear in tool responses, agent transcripts, the
address bar, and browser history without transferring any authority. Opening
one without a session mints a session — which is what a single-user local tool
should do — but the *session*, not the URL, is thereafter the authority. A
second browser on the same machine gets its own session and its own audit
trail.

**Browser participant sessions.** A same-origin loopback document navigation
with no session cookie mints one: an `HttpOnly`, `SameSite=Lax`, `Path=/`
cookie naming a durable row that stores only the SHA-256 of the cookie value.
Sessions survive a restart, have no idle expiry (a local tool must not log its
user out mid-decision), expire absolutely after 30 days, rotate when an
assurance change binds a verified principal, and are revoked with
`tangent --revoke-participant-sessions`. A minted session receives `view`,
`draft`, `resolve`, and participant-cause `cancel` — not `close`, not
`administer`.

The `/ws` upgrade requires a valid session immediately, with no grace period.
That is the change that stops a room UUID from being an answer credential. A
raw WebSocket client with no cookie receives an ordinary 403.

**Caller scope is `<authority>:<partition>`.** The authority is assigned by the
receiving adapter from admission facts and comes from a closed set; the
partition is the caller's declared application id. Two authorities exist:
`standalone-local` for every direct loopback caller, and `gateway:<binding_id>`
when a trusted in-process adapter has established a verified binding. A caller
that declares no application id is `standalone-local:anonymous`. Pre-grammar
spellings (`direct-loopback:<app>`, bare `standalone-local`) are read through a
fixed alias with no data rewrite.

> **`standalone-local` partitions are advisory, not a security boundary.**
> Any local caller can assert any partition, because the partition is the
> caller's own declared application id and nothing verifies it. Partitions are
> enforced **only across authorities**, where the authority prefix is
> host-assigned. A `standalone-local:a` caller is prevented from colliding with
> `standalone-local:b` by accident; it is not prevented from claiming to be
> `standalone-local:b`. Nothing downstream may present a `standalone-local`
> partition as isolation. ADR 0002 §6 carries the same limitation into
> retention: a per-partition deletion filter is a convenience for the local
> user, never a guarantee that one application's content has been isolated
> from another's.

**What is enforced.** Reads (`session_list`, `session_get`) stay
authority-wide, so "show me all my rooms" is unchanged. `session_close` and
`surface_close` are partition-scoped, because closing dispositions another
caller's pending human work. Cross-authority access is denied everywhere.
Unauthorized access returns **403 within an authority** and **404 across
authorities**, so a foreign authority cannot probe for existence while a local
user still gets something debuggable.

**Browser APIs.** `/api/hitl/*` and `/api/rooms` are the only routes the SPA
calls. Both are participant-session authenticated, origin guarded, and
`Cache-Control: no-store`. The same-origin guard also covers `/mcp`, `/sse`,
and `/ws`; it permits header-less non-browser clients, so MCP clients are
unaffected. Every response carries `Referrer-Policy: no-referrer`,
`X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, a
`Cross-Origin-Opener-Policy` and `Cross-Origin-Resource-Policy` of
`same-origin`, a `Permissions-Policy` denying every powerful feature Tangent
does not use, and a `Content-Security-Policy`.

## Renderer trust classes and presentation sandboxing

Implements [ADR 0003 §2.3 and §2.7](adr/0003-definition-and-package-ownership.md)
as reduced by [ADR 0009](adr/0009-renderer-trust-reduced-to-isolation.md). The
full model is [`renderer-trust-classes.md`](renderer-trust-classes.md).

**A manifest declares `renderer.isolation`** — where the renderer's code runs —
and Tangent validates it against the declared renderer shape, refusing a
manifest whose two disagree in either direction. It never substitutes one, so a
definition that materialized runs exactly where it said.

There is no capability ceiling. ADR 0009 removed the five-value trust class and
the ordered ceiling with it: read against the distribution that exists, the
class sorted seventeen first-party React components from one first-party React
component that imports tldraw, and what blocked an out-of-tree publisher was a
signature verifier that was never built. What refuses a capability now is host
policy's grant and the mediation table behind it.

**Untrusted code runs in an opaque origin, and that is untouched.** A
`sandboxed-frame` renderer draws
inside `sandbox="allow-scripts"` with no `allow-same-origin`, no
`allow-downloads`, and no `allow-forms`, under a `default-src 'none'` frame
policy whose `script-src` is a hash of Tangent's own shim — so agent-authored
scripts in a preview do not execute at all. Messages back are accepted only when
source, origin, nonce, and schema all check out; a check that is skipped when
its input is absent is not a check.

**The document CSP is what made `network.fetch` real.** `connect-src` admits
this origin and its own WebSocket schemes and nothing else, so no renderer can
reach an external origin. `clipboard.write` and `export.download` remain
enforced only inside a frame — no CSP directive covers either — which is why
`effect.Mediation` is a function of the capability *and* the isolation, and why
every receipt records both.

## Host-mediated capabilities

Implements [ADR 0003 §2.5](adr/0003-definition-and-package-ownership.md). The
full model, including what is enforced and what is only declared, is
[`host-mediated-capabilities.md`](host-mediated-capabilities.md).

**Two capability namespaces, one gate.** `internal/authz` answers "may this
principal perform this operation on this Tangent object" (`view`, `submit`,
`draft`, `resolve`, `cancel`, `close`, `administer`). `internal/effect` answers
"may this definition's renderer cause the host to act on the world"
(`file.read_scoped`, `evidence.preview`, `export.download`, `clipboard.write`,
`network.fetch`, `process.exec`). They are different Go types in different
packages and never substitute for one another — but they are not independent:
every effect names an object-access precondition, and `effect.Broker.Request`
requires it, the manifest's declaration, the host's grant, a scoped handle, a
participant intent, and an unused idempotency key, all together.

**A handle replaces a path string.** `effect.Handle` is host-minted, scoped to
one root, one interaction, one participant realm, and one pinned binding
digest, with an expiry and a granted-use budget. Its id is a locator on the
same footing as a room URL: possessing one grants nothing. A renderer never
sees a root id or a path. Filesystem effects resolve through `os.Root`, which
is race-safe against symlink swaps rather than checking a name and then opening
it.

**Every request writes an immutable receipt**, granted or refused, carrying a
byte count and a content digest and never any content, path, or session.

**Nothing composes an authority in the shipped binary.** `effect.Standalone()`
registers no workspace root, grants no capability, and holds no administrator —
so `definition.HostPolicy.GrantableCapabilities` is empty, no shipped
definition declares a `required_capability`, and every request through
`POST /api/effects` is refused with `effect_capability_undeclared`.
`PrivilegedActorPolicy` is wired to the same authority and denies.

## Operability: liveness, readiness, and capability health

`internal/health/` — three probes that answer three different questions. A
single `/healthz` that returned 200 while the store, migrations, registry,
renderer host, or a requested kind was unavailable converted a loud failure
into a silent one; separating them is what closes that.

| Surface | Question | Touches |
|---|---|---|
| `GET /healthz` | Is this process responding? | Nothing. No database, no lock, no registry read. |
| `GET /readyz` | Can it serve traffic right now? | Database ping + one statement, schema version against the version this binary embeds, definition registry, renderer host, delivery-worker authorization. |
| `GET /healthz/capability` | Which kinds can it serve? | The materialized registry. Bounded listing of everything unservable. |
| `GET /healthz/capability/{kind}` | Can it serve *this* kind? | One definition's materialization state and effect posture. |
| `tangent.health_report` (MCP) | All three, over the channel the work arrives on. | The same reporter. |
| `tangent.telemetry_query` (MCP) | What happened to this invocation, this kind, or this check? | The append-only `telemetry_events` table, plus the in-process metric snapshot. |

**Liveness deliberately touches nothing.** The managed-runtime health probe in
the repo-root `tangent.cerberus.yaml` points at `/healthz`, and that
probe feeds a supervisor's restart decision. Pointing a supervisor at readiness
turns one slow query into a restart loop, so `/healthz` keeps its path and its
`{"status":"ok"}` body, and readiness is what an operator and
`cerberus resource doctor` read instead.

**Readiness returns `ok`, `degraded`, or `unavailable`.** Degraded still serves
— a quarantined kind on a host that serves seventeen others is not an outage —
and answers 200; only `unavailable` answers 503.

**Capability health reuses the materialization-state vocabulary** from
`internal/definition` rather than inventing a second one. `incompatible`,
`quarantined`, `unavailable`, and an intermediate state are four different
fixes, and a report that collapsed them into "unhealthy" would cost an operator
the difference. It also reports the effect posture truthfully: with no manifest
declaring a capability, every kind reports
`effect_request_outcome: effect_capability_undeclared` rather than silence,
because silence reads as "effects work".

**Every failing check carries an operator action, and no report carries
content.** A health response is read during an incident and is the most likely
thing in the process to be pasted into a chat window, so it names no path, no
payload, no participant text, no session, and no capability material — only
host-published facts and one sentence saying what to do. Detail strings, the
per-kind listing, and the whole response body each have a ceiling.

## Correlation and payload-safe telemetry

`internal/telemetry/` — one caller invocation gets one identity that survives
every boundary it crosses, and what happened to it is recorded somewhere that
is not the process log.

**The identity is derived, not propagated.**

```
trace_id = SHA-256("tangent/interaction-trace/v1" ‖ caller_scope ‖ idempotency_key)[:16]
```

Both inputs are columns on `interactions`. A context-propagated trace id would
survive none of the boundaries that matter here: the browser is a separate
process, the store outlives every process, and a delivery can happen after the
transport that asked for it is gone. A derived identity is recomputed instead —
by the MCP adapter that admits the request, by the WebSocket handler that
refuses a stale frame, by the delivery that hands the outcome back, and by a
health report naming a broken kind — so a restart, a refresh, and an idempotent
retry all land in the same trace without anything having carried a header.

**Gateway trace context is a link, not the identity.** A gateway in front of
Tangent (Tether's `mux` proxy) forwards each tool call with the caller's W3C
`traceparent` written into the arguments as `_traceparent` (and `_tracestate`).
The MCP boundary (`internal/mcp/gateway_metadata.go`) removes exactly those two
keys before strict schema validation, so closed schemas stay closed and an
unknown key is still refused, and it attaches the parsed context to the
request. Every observation emitted while serving that call then carries
`upstream_trace_id` and `upstream_span_id` as record-only attributes and an
OpenTelemetry span link to the caller's span. The derived identity is
unchanged: the upstream trace never replaces `trace_id`, never becomes a metric
dimension, and is dropped rather than recorded when it is malformed.

**How far it genuinely reaches.** The trace covers the server's observation of
every stage: admission, definition resolution, presentation, the participant's
terminal action as the server received it, persistence, and delivery. It does
**not** contain browser spans. The renderer's own work — paint, interaction,
submit — is not instrumented, and Tangent deliberately does not accept a
client-supplied trace header, because a trace id asserted by a tab is a
correlation identifier the host cannot vouch for. Connection lifecycle and
per-kind renderer failures are filed under their own derived traces
(`TraceForRoom`, `TraceForKind`) rather than being forced into an invocation's,
because neither is part of one request.

**Audit records are a table, not a log.** `telemetry_events` (migration 0011)
is append-only, indexed by trace, interaction, room, event, and time, and read
through `tangent.telemetry_query`. It is separate from the existing
`surface_events` / `interaction_events` / `delivery_events` journals for three
reasons: a telemetry write must never be able to fail the operation it
observes, half of what has to be counted (a lease refusal, a failed
compare-and-set, a capability denial, an unservable renderer) advances no
record revision and therefore has no journal row, and a lifecycle journal has
no trace identity and cannot acquire one without altering an immutable audit
table. Retention is bounded by age and row count and swept at boot; `DELETE` is
permitted on this one audit table for exactly that reason, `UPDATE` is not.

**Redaction is structural.** ADR 0002 §8 is implemented as a type rather than a
convention: a closed attribute-key allowlist, closed value vocabularies per
key, an identifier shape that rejects paths, URLs, and prose outright, and —
the load-bearing part — **no free-text field anywhere**. There is no `message`,
`detail`, `reason`, or `error` key, so `err.Error()`, a terminal reason, a
close reason, a participant's words, and a SQLite error carrying the database
path have nowhere to go. Failures are recorded as *typed codes* drawn from a
closed set; anything else becomes `unclassified`. Keys the allowlist does not
name are dropped and counted, and a test asserts that count is zero across the
shipped paths.

**Metrics** cover presentation and resolution latency, reconnects, stale
clients, delivery lag and retries, draft conflicts, renderer failures, and
capability denials — each sourced from the mechanism that already owned the
fact, never from parallel state. Trust-class denials and host-policy denials
stay separate, because widening host policy lifts one and does nothing to the
other. They live in a bounded in-process registry (lost on restart, which is
what the durable aggregate is for) and are mirrored to OpenTelemetry.

**OpenTelemetry is the API only, and off by default.** Tangent depends on
`go.opentelemetry.io/otel` and its `trace`/`metric` sub-modules and on nothing
else from that ecosystem: no SDK, no OTLP exporter, no gRPC, no protobuf. With
no SDK installed the global providers are no-ops, and the bridge is
additionally gated behind `TANGENT_OTEL` because those providers are
process-global and a single-user loopback host must not start exporting because
some other dependency installed one. An embedder turns it on by adding an SDK
to their own module, installing it before Tangent records anything, and setting
the flag — no instrumentation changes.

**Health links failures to safe correlation identifiers.** Every non-passing
readiness check and every unusable kind carries a `correlation` block naming
the trace its history is filed under and `tangent.telemetry_query` as the
reader. Readiness observations are emitted on *transitions* only, so a
supervisor polling on a timer does not bury the moment something changed.

## Current limitations

Stamped against `ce4aca8`. This is the canonical list — `README.md` and
`developing.md` point here rather than keeping their own
copies. Every entry is either a permanent scope decision or names the Torque
task that closes it. **An entry that omits a limitation is worse than an
entry that admits one:** the direction document this repository just retired
was deleted precisely because its status half rotted while its readers kept
trusting it.

### Scope decisions (not defects)

- **Localhost only, single-user.** No remote access. Authorization is
  object-scoped (ADR 0004) but loopback admission is not authentication: a
  hostile local process running as the same user can still mint a participant
  session. Tangent moves authority off the URL; it does not defend against
  that.
- **One active pending envelope per room.** History persists, but a room holds
  one envelope awaiting submission at a time. An overlapping
  `tangent.session_advance` is refused with `SESSION_BUSY` rather than queued.
  This is a property of the compatibility room projection, not of the durable
  substrate beneath it.
- **Whiteboard is single-user localhost first.** The board is shared between
  one user and one agent through one persistent room; there is no live
  multiplayer presence or conflict resolution.
- **Spreadsheet review is review-only, not a spreadsheet editor.**
  Agent-provided rows are canonical; no formulas, workbook semantics, arbitrary
  cell editing, or remote spreadsheet connectors.
- **No desktop shell and no Nanite-native channel.** MCP is the only
  agent-facing transport. See the Wails note above.

### Plugin lifecycle and configuration boundary

Plugin loads receive an owner-scoped registration handle. Unload closes that
owner's admission before removing its MCP tools, HTTP routes and contributed
kind registrations, then invokes real subprocess teardown. A retained handle
cannot register into a replacement owner. Core definitions and other owners
are never swept; versioned definition material remains available for retained
interactions already pinned to it.

`GET /api/plugin-management` reports current installed lifecycle state and
desired enable intent. Participant-guarded `POST` actions at
`/api/plugin-management/{pluginID}/enable`, `/disable` and `/reload` serialize
lifecycle operations. Disable intent survives host restart in the boolean-only
`.state/enabled.json` under the install root. Reload stops the old owner before
rescanning and snapshotting the installed artifact; every new child receives a
fresh host-issued incarnation and generation. A failed replacement is reported
failed and does not revive old registrations.

A dispatch that exhausts the host budget trips that owner's circuit: further
calls refuse immediately, and teardown stops and reaps its subprocess. The
failed/quarantined state remains visible until explicit enable/reload or a new
host startup. Caller
cancellation does not trip a healthy owner's circuit. In-process test adapters
cannot have an uncooperative Go goroutine forcibly interrupted; they are fenced
and receive no additional work. Installed plugins run out of process.

Reviewed manifest settings have a private host store and OS-keychain-backed
secret references. Browser responses expose secret presence only. Each child
attempt resolves its own immutable Init.Config snapshot; current owner handles
can access only their declared keys. Global config calls remain refused. Save
uses a revision CAS; apply/restart checks that exact revision under the lifecycle
operation gate and only successful load/registration marks it applied. Enable
intent remains boolean-only and separate. See [plugin configuration](plugin-configuration.md).

### Browser plugin registry integration

The SDK pin requires registry v2 on the empty `/api/plugins/registry` proof
endpoint. It shares the lifecycle host epoch and serves immutable revision 1;
this is not yet a populated contribution catalog. The current browser loader
still speaks registry v1 and refuses that snapshot. Its fire-and-forget sync
does not block application boot, and no plugin UI contributions are currently
served. Browser registry adoption and catalog population remain follow-up work;
this driver change makes no frontend changes.

### An additive version bump takes pending interactions out of service

ADR 0003 §3 makes any change that moves `contract_digest` a `version` bump, and
§8 C1 makes a registry change that alters the current binding render the pinned
definition `unavailable` for **new submissions**. Adding an optional field to a
shared kind therefore takes live interactions of that kind out of service, even
though the old payloads are still valid by construction.

The conservatism exists because the host cannot generally prove a pending
payload still satisfies a moved contract. For a `compatibility_class: additive`
change with the new fields optional it demonstrably can, so narrowing C1 for
that case is the honest fix. Chrispian's call (`CW-20260911-0008`) is to proceed
and fix the breakage as it is felt rather than pre-emptively, which is
reasonable at 0.x with one user — and it is why
`docs/writing-a-plugin.md` tells an author to budget for the blast radius before
widening a shared kind. File the narrowing as its own task when the churn is
felt.

The Agent Turns definition `1.1` (CW-20261002-0134) is an explicit instance:
its optional stage/source fields keep stored `1.0` bodies readable, but do not
rewrite their pinned bindings or grant them new operations. The response/definition `1.2` (CW-20261002-0133) adds explicit immutable
interrupt intent with the same pending-binding limitation. Plan the pending-item
blast radius before installing either host version.

### `standalone-local` partitions are advisory, not a security boundary

Any local caller can assert any partition, because the partition is the
caller's own declared application id and nothing verifies it. Partitions are
enforced **only across authorities**, where the authority prefix is
host-assigned. A `standalone-local:a` caller is prevented from *colliding*
with `standalone-local:b` by accident; it is not prevented from *claiming* to
be `standalone-local:b`.

**Nothing downstream may present a `standalone-local` partition as isolation.**
ADR 0002 §6 carries the same limitation into retention: a per-partition
deletion filter is a convenience for the local user, never a guarantee that one
application's content has been isolated from another's. See "Room access and
caller scope" above.

### Declared but not exercised

- **No definition declares a host-mediated effect capability.** Every request
  through `POST /api/effects` is refused with `effect_capability_undeclared`,
  and `/healthz/capability` reports that posture explicitly rather than
  silently. The broker, its handle minting, its receipts, and its refusal
  paths are covered by tests, and have **zero production traffic**. The
  designed posture is not the same as a proven one (`CW-20260905-0010`).
- **`clipboard.write` and `export.download` are enforced only inside a
  sandboxed frame.** No CSP directive covers either, so on the main origin they
  remain *declared, not enforced* — which is why `effect.Mediation` is a
  function of the capability **and** the isolation, and why every receipt
  records both. `network.fetch` is the one that became genuinely enforced,
  by the document CSP's `connect-src`.
- **`renderer.entry` loads nothing.** A manifest's renderer entry is recorded,
  digested, and pinned, but no loader consumes it; `ui/src/main.tsx` registers
  renderers by string literal. The trust class therefore constrains a renderer
  the host already shipped, not one a publisher delivered
  (`CW-20260905-0004`).
- **`SaveDraft` has no production caller.** Browser `localStorage` is the only
  draft custody actually running. Drafts are therefore per-browser, invisible
  to the durable retention model, and not covered by the custody guarantees
  ADR 0002 describes (`CW-20260905-0001`).
- **The ADR 0002 §3 custody-precedence engine is not implemented.** Retention
  operates on host windows only; the precedence rules that would let a
  publisher or a caller override a host window are specified and unbuilt
  (`CW-20260905-0008`).

### Proven by construction, not observed

- **There is no browser in CI.** The CSP, the frame sandbox, the opaque origin,
  and the `postMessage` checks are verified by unit tests over the emitted
  policy strings and by code inspection — not by watching a real browser refuse
  anything. [`manual-tests/renderer-sandbox-e2e.md`](manual-tests/renderer-sandbox-e2e.md)
  is the actual verification and it is manual. Treat a passing CI run as
  evidence the policy is *emitted*, never as evidence it is *enforced*
  (`CW-20260904-0171`).
- **The OpenTelemetry path has never been observed against a collector.**
  Tangent depends on the OTel API only — no SDK, no exporter — so with no SDK
  installed the global providers are no-ops and nothing is emitted. The bridge
  is additionally gated behind `TANGENT_OTEL`. Nobody has yet run it with an
  SDK installed and watched spans arrive somewhere (`CW-20260905-0011`).

### Open work

- **The cooperative relay inbox has no manually-launched-agent proof yet,
  and no operator-side surface.** The relay tools (`CW-20260906-0066`)
  ship the agent-facing MCP surface only; proving the loop end to end with a
  real launched Claude session is `CW-20260906-0071`, a built-in CLI relay
  provider and launcher skill is `CW-20260906-0072`, the operator's own
  channel pane is `CW-20260906-0017`, and attention-entry projection over
  relay messages is `CW-20260906-0067`.

Everything above that is not a scope decision has a Torque task. Further open
direction lives in the twelve `CW-20260905-*` tasks that
[ADR 0005](adr/0005-product-boundary-and-portfolio-composition.md) opened when
it retired `docs/interactive-collaboration-direction.md`. That document is
**deleted**; its durable half is ADR 0005 and its status half was not
preserved. Do not re-create it — an idea that is worth keeping goes to a Torque
task or into an ADR, not into a file that mixes decisions with observations.

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

## Unified Inbox and room presentations

The default browser view is Inbox. Approvals, documents, agent turns and
structured room workflows share one global arrival sequence, assigned in the
same transaction that creates each canonical interaction. The `inbox_order`
index stores identity and order only; requests and resolutions remain in the
canonical substrate. Existing records are backfilled by creation time and
insertion order. `/api/inbox` is a pure, participant-guarded authority-wide local
projection; it claims no resolver lease and records no caller retrieval or ack.

Filters, search and newest-first sorting change the view only. The default is
pending work in FIFO order. Selecting an item opens its kind's interactive body
in the expanding main pane. Completed items retain original request context
and confirmed responses in History. Retention still governs the underlying
content; browsing history is never resubmission.

Navigation offers Inbox, Channels and Settings rather than a tab per room.
`/inbox/items/<interactionID>` identifies an exact request. `/r/<roomID>` opens
the latest interaction in that room, including its response if completed.
Rooms remain presentation containers: a live structured body attaches through
the existing WebSocket bridge, with revision checks and the resolver lease.
Switching requests, refreshing or closing the browser does not cancel work.
Channels' relay semantics are unchanged. See [`inbox.md`](inbox.md).

## External resource reviews

The domain-free `tangent.external-review` kind displays a retained resource
snapshot, agent notes, source link and plugin-served actions. Clients submit it
through `tangent.session_advance`. The renderer accepts only same-origin plugin
routes; the plugin owns application reads, writes, authority and revision checks.
Opening a review never executes an action. Historical renderers run read-only
and do not refresh or execute plugin commands. See [PR reviews](github-pr-review.md).
