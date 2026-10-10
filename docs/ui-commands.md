# Host view descriptors and UI commands

CW-20261009-0086 implements the host substrate of [ADR 0014](adr/0014-ui-commands-and-view-descriptors.md).
`internal/uicommand` owns validation, the per-attachment descriptor cache,
command declarations, latest-active targeting and acknowledgement correlation.
`internal/ws` adapts the existing room WebSocket; neither envelopes nor drafts
are pushed or resolved by this channel.

## Authority and current integration boundary

Production does **not** install this channel. A trusted adapter must establish
an opaque non-credential participant-session reference, participant and
conversation for the exact server-issued browser attachment through
`ws.SetUICommands` and `UIAttachmentResolver`. Cookie values, cookie hashes,
client IDs, caller labels, room resolver leases and source metadata cannot
supply this binding. There is no production verified conversation binding
provider in the merged capability implementation. Nil or incomplete resolvers
refuse; no fallback to the room participant resolver exists.

Agent reads and commands independently require a trusted `uicommand.Authorizer`
that derives the same verified binding from the calling context for every
operation. It accepts no agent-supplied targeting arguments. Missing or
incomplete bindings refuse both disclosure and delivery. Tests supply synthetic
verified bindings only. This is not end-to-end agent readiness.

CW-20261009-0090 adds React publication, route adoption, router dispatch and
accessible read-only modals. CW-20261009-0091 adds MCP source exposure. React
uses the existing room transport or an explicitly injected transport; neither
path establishes the production verified authority provider. Existing WebSocket upgrades remain room-scoped;
this task does not add a global socket endpoint or create rooms on attachment.

## MCP source adapter (CW-20261009-0091)

`mcp.WithUICommands` injects the same broker used by `ws.SetUICommands`,
before serving. Omission returns `ui_unavailable`; a broker with absent or
denied verified authority returns `ui_forbidden`. Production omits this option.
The shipped tools are `tangent.view_get`, `tangent.ui_navigate`,
`tangent.ui_open_modal`, `tangent.ui_close_modal`, `tangent.ui_open_drawer`,
`tangent.ui_close_drawer` and `tangent.ui_focus_item`. Inputs are the core-owned
closed argument objects, with no participant/session/conversation selectors.
View reads return the fresh detached descriptor, revision and control setting,
without binding or attachment identifiers. Reads remain allowed when control
is disabled. Authority is rechecked before disclosure and command delivery.

Only reviewed core definitions install MCP tools; view/plugin descriptive
schemas cannot install handlers or grant access. View-specific commands such
as `set_filter` and `open_doc` remain unexposed in this bounded slice; they
require reviewed host tool definitions before MCP adoption. The current view
must declare any core command it accepts. Unknown arguments refuse before
delivery and undeclared core commands return `ui_rejected`. Browser ack results
retain command ID, exact view revision, status and bounded reason. Missing
view returns `not_visible`; disabled control returns `ui_disabled`; missing
ack returns a `timeout` tool error. Cancellation returns `ui_canceled`; delivery
or validation failure returns `ui_rejected`, without echoing arguments. A
timeout or cancellation cannot prove the browser did not apply the command.
No provider, global endpoint or live integration is supplied here.

## Descriptor v1 and observation projection

A route/plugin publishes a **display observation**, with no secrets or authority:

```json
{
  "type": "view.publish",
  "descriptor": {
    "version": 1,
    "route": "/inbox",
    "active_filters": [{"name": "status", "values": ["open"]}],
    "search": "release",
    "selected_ids": ["synthetic-item"],
    "visible_rows": [{"id": "synthetic-item", "summary": "Release notes"}],
    "available_commands": [{
      "name": "open_doc",
      "scope": "ephemeral",
      "input_schema": {
        "type": "object",
        "properties": {"id": {"type": "string"}},
        "required": ["id"],
        "additionalProperties": false
      }
    }]
  }
}
```

Only `version` and `route` are required. Route is a pathname, excluding query
and fragment. Display filters have string values; richer filters need a new
coordinated version. Accepted publication returns `{"type":"view.published","view_revision":N}`
to that attachment so the browser can track the host revision. Invalid input
refuses atomically, preserving the prior view. No truncation or silent clamping occurs.

| Bound | Limit |
| --- | --- |
| Received and normalized descriptor/client_context | 32,768 UTF-8 bytes |
| JSON nesting / value nodes | 16 / 2,048; root depth 0, each value/container counts, key tokens do not |
| Route | 256 bytes, origin-relative pathname, no whitespace/control |
| Active filters | 16 unique names, 64 bytes/name; 1–16 values, 256 bytes/value |
| Search | 1,024 bytes |
| Selected IDs | 64 unique IDs, 128 bytes/ID |
| Visible rows | 32 unique IDs, 128 bytes/ID, 512 bytes/summary |
| Available commands | 16 unique names, 128 bytes/name |
| Normalized input schema | 2,048 bytes |
| Descriptor publication | At most one attempt per 100 ms per attachment |
| Attachments / in-flight commands | 256 / 64 per broker |
| Command arguments / ack reason | 8,192 / 512 bytes |

Unknown fields, duplicate JSON keys (also inside schemas), invalid UTF-8,
lone surrogate escapes, null observation fields, duplicate named entries,
wrong types and overflow refuse. Names/IDs exclude whitespace; non-printing
controls refuse except newline/tab in display text. Schema numbers retain their
JSON representation. Errors never echo submitted descriptor/argument values.

`Broker.Get` returns a detached latest snapshot for the verified binding.
`Broker.Observation` obtains a fresh authorized read and projects exactly
`{"version":1,"view":{...observation fields...}}`, excluding the host revision,
control setting and all binding/attachment fields. It checks the bounds again
because the projection adds an envelope. This matches the proposed Nanite
CW-20261009-0084 view-observation proposal (Nanite acknowledgement pending;
no shared endpoint is ratified here); descriptive schemas there are data,
not installed tools or authority.

The host caches a latest descriptor only until attachment detach. It does not
persist it, log its body, or push descriptor changes to agents. A per-turn
consumer must obtain a fresh verified observation for every turn and retry,
capture an immutable process-local snapshot, and omit context on absence rather
than reuse an earlier view. That snapshot must not be persisted in transcripts,
compaction or session state. The provider may receive and quote observations;
validation cannot prove display text contains no secrets. Consumers own their
per-turn retention and input-budget checks.

## Registry, activity, control and acknowledgement

Core declarations are returned by `uicommand.CoreCommands()`: `navigate`,
`open_modal`, `close_modal`, `open_drawer`, `close_drawer`, `focus_item`. Every
view can opt in to them but must declare acceptance; core scopes and schemas
cannot be overridden. `navigate` accepts `{ "route": "/local/path?filter=open" }`;
the others accept `{ "id": "visible-target" }`. Views may add commands such as
`open_doc`. Host command names use lowercase identifiers with an optional
plugin prefix separated by a dot. An available-command observation does not
promise any corresponding MCP tool exists.

Effectful declarations use a closed, local schema subset: object/array/string/
boolean/integer/number types, properties, required, enum, items, bounds, and
`additionalProperties: false`. References, patterns, combinators, unknown
keywords and schemas deeper than six levels refuse. No remote schema is fetched.
The root arguments schema must be a closed object. The descriptive observation
contract permits arbitrary bounded schema objects; the Tangent **effectful
registry** accepts this narrower subset.

The browser sends `{"type":"view.active","active":true}` on foreground/user
activity and `false` on loss of visibility. Activity is not inferred from
heartbeats or publications. Commands choose the most recently active attachment
with the exact participant/conversation/session binding; a background publisher
cannot steal selection. An opaque or disabled newest view refuses rather than
falling back to an older enabled tab. Detach removes its descriptor and permits
selection of another active attachment of the same binding.

Control starts disabled on every attachment. Only participant traffic
`{"type":"ui.control","enabled":true}` enables it. Disabling cancels pending
commands with a rejected result but leaves authorized descriptor reads available.

The host emits to **one** attachment:

```json
{"type":"ui.command","command_id":"host-issued-id","view_revision":1,"name":"focus_item","scope":"ephemeral","arguments":{"id":"synthetic-item"}}
```

The browser checks the revision, current view, control setting and scope, then
returns after application:

```json
{"type":"ui.ack","ack":{"command_id":"host-issued-id","view_revision":1,"status":"applied"}}
```

Statuses are `applied`, `not_visible` or `rejected`, optionally with a bounded
reason. Only the chosen attachment and current revision can acknowledge;
peer-tab, stale, malformed and duplicate acknowledgements refuse. Publication,
disabling, inactive notification or detach rejects pending work. Timeout (five
seconds by default, configurable up to thirty), cancellation and send failures
return explicit errors. A successful socket write is never application success.
A timeout cannot undo a command already applied; callers must not blindly retry.

Ephemeral scopes never carry history instructions. URL-backed commands use the
router, including navigation and filters already encoded in the URL. Navigation
arguments may include query/fragment, unlike the descriptor pathname, but must
remain local. The browser adapter must enforce ephemeral handlers cannot change
history and must refuse unsupported targets; the Go host cannot observe browser
history. The React adapter guards synchronous ephemeral history calls and
checks the unchanged URL; the actual Chrome acceptance covers modal behavior. No data-writing or
destructive application action belongs in this registry.

Verification uses synthetic descriptor/attachment fixtures: strict validation,
registry/argument refusal, authority denial, control opt-in, multi-tab targeting,
ack correlation/statuses, cancellation, timeout, detach and actual WS round trips.
Existing room tests protect the independent pull-only draft path.


## React adoption (CW-20261009-0090)

`UiChannelProvider` creates no socket, room, participant or conversation. The
existing `Room` hands it the presentation-only adapter on its exact `WSClient`.
An embedder may inject that interface for synthetic tests or a future verified
host integration. Unattached routes report unavailable; host refusal disables
control and requires a new attachment before retry. Nonadopting routes retire
activity and control. Production keeps the same unwired, default-refusing
binding seam described above.

`useUiCommands` couples each declaration to argument validation and a synchronous
presentation handler. `useViewDescriptor` publishes on committed changes after
a debounce longer than the host's publication interval. One publication is
outstanding per transport; its acknowledgement remains correlated across route
replacement and gaps with no adopter. A changed view immediately fences the old
revision. Reconnect clears revisions and participant control opt-in. Commands
check the exact revision/current observation, foreground focus, opt-in, declared
scope, arguments and explicit visible targets. A bounded duplicate fence applies
within a revision. Acknowledgement follows `flushSync` application, including
modal rendering and focus. Route retirement caused by a command follows its ack,
so it cannot cancel a successfully applied navigation before acknowledgement.

The declarative router uses `useTransitions={false}` so URL-backed handlers commit
within that application boundary; a queued router transition cannot count as an
applied route. Ephemeral handlers cannot call `pushState` or `replaceState` while
applying. Navigation accepts only mounted application paths; unsupported paths
refuse before router application. Commands perform no data writes. Accepted commands are:

| View | Ephemeral | URL-backed |
| --- | --- | --- |
| Inbox | `open_modal`, `close_modal`, `focus_item` | `navigate`, `set_filter`, `set_search`, `select_item` |
| DocsInbox component | `open_modal`, `close_modal`, `focus_item`, `open_doc` | `navigate` |
| Channels | `open_modal`, `close_modal`, `focus_item` | `navigate` |

DocsInbox's existing local filters remain local. Unified Inbox continues to own
the public document route; adoption does not restore a separate Docs route.
Drawer commands are undeclared because these route adapters provide read-only
modals, not drawers. Unknown or hidden targets return explicit refusal.

Observations deliberately sample at most the first 32 displayed targets, with
IDs and fixed category summaries rather than titles, document bodies, message
previews, source metadata, credentials or draft input. Selected IDs, fixed display
filters (with unsupported URL values represented by a fixed marker), search
presence and modal/queue visibility are opt-in route observations
under the v1 limits. Raw participant search text is withheld; a local-only view
key still fences its changes even when the observation stays identical. Oversized or malformed observations refuse as a whole; user state
is never silently shortened. An embedder authoring a different descriptor remains responsible for excluding
sensitive display values: schema validation cannot prove text contains no secrets.
Consumers must still follow the per-turn transient retention rules above.

Channels preserves successful ordinary participant selection, direct-link and
back/forward read behavior. Command-origin navigation suppresses the automatic
read receipt: changing what is displayed is not a message read/write command.
Participant navigation clears that origin marker. Focus and modal commands never
record channel receipts, send messages or perform HITL/Docs actions.

Read-only modals use the existing Radix dialog primitive: named dialog, focus
entry, keyboard trap, Escape close and restoration to the prior connected focus
target. Unsupported focus targets and targets hidden by a modal refuse.

Verification includes component tests and the existing WS client/lifecycle tests.
Run `heavytest node scripts/ui-commands-browser.mjs` with the supported Node and
a private `TMPDIR` for actual Chrome acceptance; set `TANGENT_BROWSER_BIN` to a
Chrome executable on other hosts. It creates an isolated browser profile and a
loopback Vite fixture, tests the production Inbox/Channels components using an
injected synthetic transport, then removes its scratch. It verifies modal URL
and history invariance, focus entry/trap/restore, router filter/navigation commit
and absence of writes. It does not exercise a live Tangent, real binding provider,
MCP command exposure or end-to-end agent readiness.
