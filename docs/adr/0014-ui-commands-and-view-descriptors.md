# ADR 0014: UI commands and view descriptors

**Status:** Accepted by Chrispian (DEC-061), 2026-10-09. Source reconciliation: CW-20261009-0083, 2026-10-10.
**Date:** 2026-10-09
**Amends:** ADR 0007 section 5 ("Reading view state is a pull. Tangent does not push").
**Related:** ADR 0013 (chat agent plugin), 0008, 0009 (renderer trust); Torque epic EP-20261009-0003.
**Evidence base:** Tangent `43dea0cc`, 2026-10-10. Acceptance is PM message `01a1231d-8439-7c02-8dac-e032671dc013`; earlier workspace proposed status was stale. [R] source read, [U] unverified integration. The decision below describes required host work, not an existing UI-command API.

## Context
The chat agent must know what the user is looking at and change what they see: set filters or search, open or close modals and drawers, open a document viewer, go to another page. Today [R]:
- Filters, search and sort are client-side view state (architecture.md). The room WebSocket already presents agent-submitted envelopes and captures revision-pinned responses, but there is no general participant-targeted UI-command path. The browser saves drafts and the agent pulls them with `surface_get`; only `app-board` is server-custodied.
- The shell has no common state store; routes manage their own state. `channel.ViewFocus` exists in the store but no UI or tool uses it [U].
- Plugin GET/POST routes return a complete protocol-2 `HTTPResponse` body. There is no incremental flush or plugin SSE contract; declaring an SSE content type cannot make it streaming. The browser plugin loader remains a minimal proof and the registry empty (CW-20261003-0064).
Owner constraints: modals and drawers must not change the URL; filters that already live in the URL may change it; real navigation, and initial defaults, may. The agent should discover what is possible, and plugins must be able to opt in.

## Options considered
**A. Typed UI commands exposed as MCP tools, delivered over the existing WebSocket, with ack (chosen).**
**B. ACP.** An agent-to-client protocol for editor-like clients; the Nanite chat stream already covers that leg. It does not define page commands. Rejected for this purpose.
**C. Re-issue surfaces with new data** (the Torque board pattern). No host change, but cannot drive an existing screen. Kept as fallback for room-based surfaces.
**D. DOM or screenshot scraping for context, injected input events for control.** Brittle, expensive, unsafe. Rejected.
**E. Chat stream tool-call events drive the GUI directly.** Works only while the rail is open and the agent runtime is the sole path; couples the GUI to one agent runtime. Rejected as the primary design (the chat rail still renders tool calls normally).

## Decision
1. **View descriptor.** Each route or plugin view that opts in publishes a small, bounded descriptor to the host: route, active filters and search, selected item ids, a bounded summary of visible rows, available commands. Published on change (debounced) over the WebSocket; the latest descriptor per participant session is kept server-side and exposed to plugins and agents by a read tool (`tangent.view_get`) and as the per-turn client context for the chat agent. Descriptors are untrusted input (size-limited, schema-validated, no secrets).
2. **Command registry.** A view declares the commands it accepts (name, JSON schema, scope: ephemeral or url-backed). Core commands, available on all routes: `navigate`, `open_modal`, `close_modal`, `open_drawer`, `close_drawer`, `focus_item`. View commands (declared by the route/plugin): `set_filter`, `set_search`, `set_sort`, `open_doc`, and similar.
3. **Delivery (required, not implemented by current room WS).** An agent calls an MCP tool (`tangent.ui_<command>`); the host validates against the declared schema and the participant's current descriptor, pushes a `ui.command` frame to that participant's browser session over the existing WebSocket, and returns the browser's ack (`applied`, `not_visible`, `rejected` plus reason) as the tool result, with a timeout. No ack means an explicit error to the agent, never silent success.
4. **URL rules.** Ephemeral commands (modal, drawer, focus) never change the URL. URL-backed commands (filters already encoded in the URL, navigation, initial defaults) use the router. A command's declaration states which it is; the host enforces that ephemeral commands do not push history entries.
5. **Authority.** Commands must bind the verified participant/conversation and selected browser attachment; an agent-supplied session, caller label or source metadata is not that binding. Current room resolver leases do not authorize a global command to a participant. CW-20261003-0067 owns the enforcement prerequisites. Commands run with the operator's session; only commands the current view declared are accepted; a destructive or data-writing command is not a UI command (it is an ordinary tool with confirmation). A participant can disable agent control entirely in the panel (view reads continue).
6. **Pull model preserved for everything else.** Drafts and surface state remain pull-only; this ADR adds an explicit, narrow push channel for commands only.
7. **Host work.** WS frame types and ack; command registry and validation; descriptor store; `tangent.view_get` and `tangent.ui_*` tools; React hook (`useViewDescriptor`, `useUiCommands`) and adoption in Inbox, Docs and Channels; docs and the documentation gate.

## Consequences
- Every view adopting this does a small amount of work; non-adopting views are simply opaque to the agent.
- The descriptor is a new data exposure to agents: bounded, schema-checked and opt-in per view.
- Concurrency: with several browser sessions, commands target the participant session of the calling agent's conversation; multi-tab behavior needs a rule (latest active).
- ADR 0007 needs an amendment note for the narrow push.
- Highlighting in a document viewer and slides are not covered; they would be ordinary view commands once those surfaces exist.

## Open questions
Descriptor size/rate limits; how to name and version commands across plugins; multi-tab targeting; accessibility (focus management when the agent opens a modal); whether the participant must confirm the first command per session.

## Slice 0 transport reconciliation

The existing WebSocket is room-scoped (`internal/ws/handler.go`), requires
a participant binding before upgrade and checks view/draft/resolve/cancel
capabilities. Its inbound dispatch handles envelope response/cancel/draft,
resynchronization, resolver claim/release and heartbeat; unknown frames are ignored.
`internal/room/connection.go` owns concrete connections and resolver leases.
Per-connection JSON writes and room broadcasts are useful transport primitives,
not an exported plugin-to-participant command/ack port.

No current descriptor registry/store, participant-wide command broker,
command correlation/timeout/ack schema, React command hook or general
`ui.command` dispatcher implements this decision. These require the host work
in §7. Browser-applied acknowledgement must remain distinct from an envelope
resolution or a successful socket write. Multitab target selection and
participant/conversation binding must be explicit before exposing effectful
tools; existing room broadcast must not become a broadcast of commands to every
browser.

`internal/server/plugin_routes.go` buffers complete plugin replies, and
`internal/pluginhost/http.go` declares streaming unsupported. A future host
SSE proxy needs a bounded, authorized backend/stream lifetime contract; neither
that proxy nor a streaming plugin wire exists today. This source reconciliation
does not choose or implement the bridge.

`internal/authz/capability.go` holds current caller-application and participant
matrices; `internal/mcp/caller_identity.go` documents standalone-local labels
as advisory partitions. Plugin MCP tool registration and annotations do not
constitute per-tool assistant grants, and explicit empty Init grants do not
enforce the local MCP callback's authority. Descriptor reads and UI commands
must therefore acquire the scoped enforcement promised by -0067 rather than
claiming it from existing manifests or credentials.

The accepted UI-command and URL rules remain unchanged. No provider call,
browser command, grant, deployment or live WebSocket operation was performed
by CW-20261009-0083.
