# Tangent architecture (v0.6 form-collect release)

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

### go-envelopes registry

`github.com/hollis-labs/go-envelopes` v0.1.0 — the Go side of the shared
envelope catalog. Tangent loads 26 core definitions, then registers its own
extensions—including the non-renderer `tangent.hitl-item` interaction
definition—for 44 definitions in the shipped process. Extensions use the
plugin API rather than forking the registry.

`ui/src/generated/envelope-types.ts` and
`ui/src/generated/renderer-bindings.ts` are generated from all 44 shipped
definitions (`make generate-envelopes`) and CI gates them with
`make check-envelopes`. The dump tool builds its registry through the same
`extensions.RegisterAll` the server calls, so the staleness gate watches the
kinds Tangent actually renders (ADR 0003 §9 S1). Tangent-owned workflow
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
`$defs`. The dedicated `/hitl` client types live with `ui/src/lib/hitl-api.ts`
and the evidence component.

## Wails note

Today Tangent is a Go HTTP server + embedded Vite SPA, not a Wails
desktop app. Wails wrapping is a future migration: the embedded-SPA
shape is deliberately chosen so the eventual Wails wrap is mechanical
— the same Go server can run inside a Wails shell, and the same SPA
build is what the shell loads. v0.1 ships as the localhost binary so
the shape can be proven before a desktop wrapper is added.

## Room access and caller scope

Implements [ADR 0004](adr/0004-caller-participant-and-room-access-authority.md).

**A room URL is a locator, not a credential.** `/r/{roomID}`, the `/hitl`
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

Implements [ADR 0003 §2.3 and §2.7](adr/0003-definition-and-package-ownership.md).
The full model is [`renderer-trust-classes.md`](renderer-trust-classes.md).

**A trust class decides two things**: an *isolation* — where the renderer's code
runs — and a *capability ceiling* over the effect namespace, ordered
`core-trusted ⊃ portfolio-trusted ⊃ sandboxed-code ⊃ declarative = external-surface = ∅`.
The ceiling is evaluated before host policy's grant, so widening
`GrantableCapabilities` widens nothing a class already closed. Isolation is the
host's derivation from the granted class, never a manifest field.

**Untrusted code runs in an opaque origin.** A `sandboxed-code` renderer draws
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
`~/.cerberus/projects/tangent.cerberus.yaml` points at `/healthz`, and that
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

## Limits (v0.5)

- **Localhost only.** No remote access. Authorization is object-scoped (ADR
  0004) but loopback admission is not authentication: a hostile local process
  running as the same user can still mint a participant session. Tangent moves
  authority off the URL; it does not defend against that.
- **`standalone-local` partitions are advisory.** See "Room access and caller
  scope" above. They prevent accident, not intent.
- **Clipboard, ad-hoc download, and renderer-initiated fetch are declared, not
  enforced.** The browser hands a same-origin renderer those powers directly,
  and there is no document CSP. `effect.Mediation` records the difference on
  every receipt; closing it needs the renderer trust classes and the CSP that
  `CW-20260825-0073` owns.
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
