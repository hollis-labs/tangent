# ADR 0001: Separate Surface, Interaction, Connection, and Delivery Lifecycles

**Status:** Accepted

**Date:** 2026-09-03

**Approved:** 2026-09-03 by Chrispian, as drafted during the HITL execution review

**Task:** `CW-20260825-0057`

**Implementation reviewed:** `f457ad1`

**Source:** [`../interactive-collaboration-direction.md`](../interactive-collaboration-direction.md)

## Context

Tangent currently uses a `Room` as the unit of persistence, browser attachment,
pending work, workflow state, and MCP result delivery. Public tools reinforce
that overlap through names such as `tangent.session_create`, a `roomID` input,
and a blocking `tangent.session_advance` call. In the current implementation:

- one `Room` contains the active WebSocket, pending response channels, phase
  state, and close state;
- losing the active WebSocket can close the room and fail pending work with
  `ROOM_DISCONNECTED`;
- an MCP request context bounds the in-memory pending response channel;
- startup turns persisted pending envelopes into `SERVER_RESTART` timeouts;
- `session_get.status` describes whether a room is loaded or closed while the
  same payload also exposes envelope, workflow-phase, and business-shaped
  projections.

Those mechanics were useful for proving Tangent's separate-window workflow,
but they conflate identities and lifetimes owned by different systems. A
browser tab closing is not a participant decision. An MCP request ending is
not an interaction cancellation. A Tangent resolution is not acceptance of a
Torque task or advancement of a Hadron run.

This ADR defines the vocabulary and lifecycle boundaries that the durable
interaction work will implement. It deliberately preserves the shipped public
surface while putting new state behind a transport-independent application
service. It does not approve the follow-on implementation tasks, define a
general authentication system, or authorize a breaking removal of the current
tools.

## Decision

### 1. Canonical vocabulary

The following terms are canonical in the domain and application layers.
Identifiers are opaque and stable; examples below name fields, not required ID
prefixes.

| Term | Definition and authority | Canonical identity and durable record |
|---|---|---|
| **Surface** | A Tangent-owned durable presentation container. It carries presentation metadata, participant bindings, ordered interactions, focus, retention, and lifecycle. A surface may be active with no browser or unresolved interaction. | Tangent-generated `surface_id`; `SurfaceRecord` plus append-only surface events. |
| **Room** | Compatibility vocabulary for the currently shipped surface projection and `/r/<roomID>` route. It is not an agent session, browser connection, interaction, or workflow run. | Existing `roomID` aliases the corresponding `surface_id`; existing room rows remain migration inputs and compatibility projections. |
| **Interaction** | One immutable caller request bound to an exact definition revision, one surface, one caller scope, an idempotency key, and external correlations. It has its own lifecycle. | Tangent-generated `interaction_id`; `InteractionRecord`, immutable request snapshot, definition binding, and interaction events. An incoming envelope ID is correlation, not the interaction identity. |
| **Connection** | One browser, desktop, or native presentation-client attachment to a surface projection. It can be replaced or lost without changing surface or interaction state. An MCP request or delivery transport is not a Connection unless it explicitly subscribes as a presentation client. | Tangent-generated `connection_id`; `ConnectionRecord` and connection events containing safe operational metadata only. |
| **Participant** | The principal whose input Tangent presents or captures, together with its identity authority, assurance, surface role, and capabilities. A participant is not a browser tab. | External `principal_ref` plus a Tangent `ParticipantBinding`. The binding records whether identity is asserted or authenticated. |
| **Draft** | Participant work that has not become a terminal response. Saving never mutates an earlier accepted revision. | `(interaction_id, draft_revision)`; immutable `DraftRevision` rows and an optional deletion/expiry tombstone. |
| **Resolution** | The immutable fact that an authorized participant submitted a valid terminal response to a specific interaction and request/definition revision. Approval denial and attention acknowledgement are resolutions; UI cancel is not. | Tangent-generated `resolution_id`; sealed `ResolutionRecord` plus the interaction transition recorded in the same transaction. |
| **Delivery** | A separately tracked attempt to expose an immutable terminal interaction outcome to a destination. Delivery can retry after the interaction is terminal. | Tangent-generated `delivery_id`; a `ResolutionDeliveryRecord` for a resolution or `TerminalNotificationRecord` for another terminal disposition, both using append-only `DeliveryAttempt` rows. |
| **Downstream outcome** | What the calling system did after receiving a resolution: for example accepted, rejected, superseded, or ignored it. Tangent never performs or infers that business transition. | Caller-owned object/revision; Tangent may retain only a typed `DownstreamOutcomeRef` or receipt supplied by that owner. |

`Envelope` remains portable protocol vocabulary. It carries a definition-shaped
request or response across a transport; it is not the durable aggregate. An
envelope can correlate with an interaction, but envelope, interaction, and MCP
request IDs are never interchangeable.

### 2. Ownership is split by authority, not by where JSON is stored

- The definition publisher owns the interaction kind, schema, renderer
  semantics, and interpretation of its responses.
- The calling application owns the purpose, external object, requested
  participant, expiry/cancellation policy, and downstream consequence.
- Tangent owns surface and interaction operational state, definition binding,
  presentation facts, Tangent-custodied draft revisions, resolution capture,
  delivery attempts, and audit.
- The participant owns the meaning of the input they submit. Tangent is
  authoritative only for the capture fact under the recorded identity
  assurance.
- The downstream business system owns acceptance, rejection, publication,
  deployment, task state, workflow progress, or any other domain transition.

A surface has an explicit `owner_scope` for access, discovery, and retention;
that field does not transfer ownership of caller business data to Tangent.
Caller-scoped surfaces remain the default for the existing workflow adapters.
An explicitly operator-scoped surface may be shared by callers, such as the
planned default `/hitl` inbox. The application service, not a transport
session, decides which surface policy applies.

### 3. Identity, correlation, and idempotency

Every durable interaction stores these concepts separately:

- Tangent IDs: `surface_id`, `interaction_id`, and later `resolution_id`.
- Definition identity: publisher, kind, immutable version/revision, and digest
  when available.
- Caller scope: application reference, optional caller principal, authority,
  and identity assurance.
- Participant binding: principal reference, authority, assurance, role, and
  granted capabilities.
- External correlations: typed opaque references such as agent, agent session,
  turn, task plus task revision, workflow run, step, conversation, or business
  object.
- Transport correlation: MCP request/session, Tether message, HTTP request,
  trace, and causation identifiers.

External references are not foreign keys into another product and do not grant
authority by themselves. Tangent preserves them without interpreting their
business state.

Idempotency is scoped to the immutable caller scope, not to an MCP session or
browser connection. The uniqueness key is `(caller_scope, idempotency_key)`.
An authenticated gateway binding establishes an authenticated caller scope.
In the first loopback-only direct-MCP slice, an explicit caller application
reference is accepted as **asserted**, never described as authenticated. A
legacy call with no explicit caller reference uses the named compatibility
scope `standalone-local`; it does not derive scope from an unstable MCP
session, agent display name, process ID, or browser tab.

New generic submission operations require the caller's idempotency key.
Compatibility adapters may add optional caller/idempotency fields without
changing their existing required shapes. When a legacy workflow or
`session_advance` request carries an envelope ID, the adapter derives the key
`legacy:<tool-name>:<envelope-id>` inside its resolved caller scope. If a
legacy adapter submits an interaction without any stable request ID, it uses a
fresh server-generated per-call key and reports that transport retry dedupe is
unavailable; it must not hash the payload and accidentally collapse two
intentional interactions. `session_create` opens a surface rather than an
interaction; its separate retry rule is defined in the compatibility table.

Current `go-envelopes.Trace.agentId`, `sessionId`, and `parentEnvelopeId`
remain observability/correlation values. On the unauthenticated direct-MCP
path they are assertions. They become authenticated identity only when a
trusted adapter supplies and records a verified binding separately.

For a new envelope submission, the definition resolver interprets
`Envelope.TypeVersion` as follows: a non-empty value must resolve to the exact
registered compatible version or the request fails with
`unsupported-version`; an empty value resolves the registry's current version
at submission time. Either way, Tangent persists the resolved `TypeSpec`
source, plugin/publisher, version, schema identity/digest, and host version in
the immutable `DefinitionBinding`. The interaction is never reinterpreted
against a later registry version. Legacy rows that persisted only type and
`Data` receive an explicitly uncertain legacy binding during migration rather
than invented provenance.

For the first loopback-only slice, Tangent materializes one fixed
`local-operator` participant binding with
`assurance=loopback-unverified` and fixed view/resolve capabilities. A room or
item URL remains a locator, not identity or authorization evidence. The
`ParticipantBinding` and transition-event vocabulary in this ADR does not
authorize pulling the broader identity, trust, capability-policy, or external
event-bus backlog into that slice. Durable transition events may initially be
stored transactionally beside their owning records rather than in a
generalized event platform.

### 4. Portfolio identity and ownership mappings

| System/client | Owns | Tangent stores | Must not be treated as |
|---|---|---|---|
| **Nanite** | Its agent definitions, conversation/session semantics, turns, and inline interaction modes; it may delegate process/runtime-session hosting to Tether. | Opaque Nanite agent/conversation/session/turn refs and any verified caller binding Nanite supplies; a surface or interaction handle returned to Nanite. | A Tangent room owner by identity coincidence, or the owner of Tangent's resolution record. |
| **Tether** | Gateway routing, messaging, provider/runtime coordination, and agent-runtime session lifecycle when composed, plus transport identity bindings and receipts. | Opaque Tether gateway/message/runtime-session refs, verified caller assertions when available, and delivery receipts. | Required for standalone correctness, participant resolution, or downstream acceptance. |
| **Torque** | Tasks, leases, review/evidence policy, task revisions, and task lifecycle transitions. | Opaque task plus revision refs and an optional downstream outcome receipt. | A state machine Tangent may advance when a participant clicks Approve. |
| **Hadron** | Workflow definitions, runs, steps, waits, gates, retries, branching, and compensation. | Opaque run/step/definition revision refs and an optional downstream outcome receipt. | Tangent phase state or an interaction lifecycle. |
| **MCP client/session** | Its transport session, request, timeout, and caller-side continuation. | Transport correlation and delivery-attempt facts. | Caller identity, idempotency scope, surface lifetime, or interaction lifetime. |
| **Browser client/tab** | One client instance and its local presentation state. | A connection identity, participant binding reference, synchronized revision, and safe connection events. | The participant principal, the surface, or authority to cancel merely by disconnecting. |

The same rules apply to a CLI or another MCP-speaking agent: standalone use is
first-class, and composition adds verified bindings without changing Tangent's
domain identities.

### 5. Surface lifecycle

```text
created -> active <-> suspended
created | active | suspended -> closed | expired
```

`closed` and `expired` are terminal. Connection presence is not a surface
state. Every transition is compare-and-set against the expected surface
revision.

| Transition | Authority | Durable fact |
|---|---|---|
| absent -> `created` | Authorized caller through `OpenSurface`, or a trusted compatibility adapter acting for it. | Insert `SurfaceRecord` with owner scope, policy, creation actor, and revision; append `surface.created`. |
| `created` -> `active` | Tangent application service after the surface is initialized and renderable. | Update `SurfaceRecord` revision; append `surface.activated`. |
| `active` -> `suspended` | Authorized caller/operator, or Tangent's pinned surface policy. Never an inferred socket loss. | Update lifecycle/reason/revision; append `surface.suspended`. |
| `suspended` -> `active` | Authorized caller/operator or policy-defined resume operation. | Update lifecycle/revision; append `surface.resumed`. |
| nonterminal -> `closed` | Caller or participant binding with close/administer capability; Tangent may close only under an explicit pinned policy. | In one transaction set `closed_at`, reason, actor, and revision; append `surface.closed`; and record each outstanding interaction disposition or its durable processing obligation. The records are separate, but the commit boundary is not. |
| nonterminal -> `expired` | Tangent policy evaluator applying the immutable effective expiry policy. | Set `expired_at`, policy reference, and revision; append `surface.expired`. Outstanding interactions transition separately. |

Closing a browser client only disconnects that connection. Closing a surface
is an explicit, auditable operation and does not manufacture a participant
resolution.

### 6. Interaction lifecycle

```text
submitted -> validated -> staged -> presented -> in-progress
presented | in-progress -> resolved
submitted | validated | staged | presented | in-progress -> canceled | expired
submitted | validated | staged -> failed
staged | presented | in-progress -> superseded
```

`resolved`, `canceled`, `expired`, `failed`, and `superseded` are terminal.
They are distinct dispositions and are never collapsed into `closed`,
`timeout`, or a transport error. A participant's typed denial is `resolved`,
not `canceled`. Terminal transitions use compare-and-set and cannot be
overwritten or reopened; follow-up work is a new interaction.

| Transition | Authority | Durable fact |
|---|---|---|
| absent -> `submitted` | Authorized caller through `SubmitInteraction`, or a compatibility adapter acting for it. | Insert `InteractionRecord` with immutable request, surface, caller scope, idempotency key, external refs, policy, and initial revision; append `interaction.submitted`. Duplicate keys return the original record. |
| `submitted` -> `validated` | Tangent validator/definition resolver. | Pin `DefinitionBinding`, validation result, and interaction revision; append `interaction.validated`. |
| `submitted`, `validated`, or `staged` -> `failed` | Tangent validator/host for a terminal validation, definition, or materialization failure. | Record typed failure, actor, and revision; append `interaction.failed` and queue a `TerminalNotificationRecord` in the same transaction. A delivery failure after resolution does not use this state. |
| `validated` -> `staged` | Tangent application service. | Update interaction revision and commit the projection/outbox obligation atomically; append `interaction.staged`. |
| `staged` -> `presented` | Tangent after an authorized client acknowledges the exact renderable projection revision. | Record presentation revision, participant binding, connection correlation, timestamp, and interaction revision; append `interaction.presented`. |
| `presented` -> `in-progress` | Authorized participant through a draft/progress operation. | Insert an immutable `DraftRevision` or typed progress fact and advance interaction revision; append `interaction.in_progress`. |
| `presented` or `in-progress` -> `resolved` | Participant binding with resolve capability, followed by Tangent schema and expected-revision validation. | Validate the exact pinned request, definition, and presented projection revisions; then in one transaction insert immutable `ResolutionRecord`, compare-and-set the interaction terminal state, append `interaction.resolved`, and insert each initial `ResolutionDeliveryRecord` in `queued`. A staged-but-unpresented interaction is not respondable. |
| nonterminal -> `canceled` | Explicit caller cancel/withdraw, explicit participant cancel when allowed, authorized administration, or a pinned surface-close policy. | Compare-and-set terminal disposition; record typed origin and cause (`caller_withdrawn`, `caller_canceled`, `participant_canceled`, `administrator_canceled`, or `surface_policy`), reason, actor, and `interaction.canceled`; queue a `TerminalNotificationRecord` in the same transaction. |
| nonterminal -> `expired` | Tangent policy evaluator applying the request's pinned expiry policy. | Compare-and-set terminal disposition with policy reference and time; append `interaction.expired` and queue a `TerminalNotificationRecord` in the same transaction. A transport deadline alone is not this authority. |
| `staged`, `presented`, or `in-progress` -> `superseded` | Authorized caller naming a replacement interaction and expected revision. | Compare-and-set old interaction, record replacement ID and actor, append `interaction.superseded`, and queue a `TerminalNotificationRecord` in the same transaction. The replacement is a separate submitted record. |

An MCP context cancellation, failed WebSocket, process shutdown, or browser
navigation only ends a waiter/connection/delivery attempt. None is authority
for an interaction terminal transition unless a separately authenticated,
explicit cancel command or pinned expiry policy is applied.

`withdraw` is the HITL/caller-facing operation for the canonical
`caller_withdrawn` cancellation cause; it is not an additional terminal state.
Participant cancellation uses `participant_canceled`. Transport cancellation
terminates only its waiter or delivery attempt and does not write either
cause. Expiry remains the separate `expired` state. These distinctions are
durable even if a legacy response projects more than one cause as
`"cancelled"`.

For built-in synchronous definitions in the first durable slice,
`SubmitInteraction` uses one database transaction to insert the immutable
request, pin the definition, write the `submitted` and `validated` events,
advance to `staged`, and commit the presentation/outbox obligation. A
successful call therefore cannot strand an externally visible `submitted` or
`validated` row after a crash. A terminal validation failure may commit the
request plus `submitted` and `failed` events for audit without becoming
respondable. Any future asynchronous definition/materialization path must
first define a durable processing obligation and restart worker for those
intermediate states; process-local continuation is forbidden.

### 7. Connection lifecycle

```text
connecting -> synchronized -> connected -> disconnected
                              |
                              +-> stale -> resynchronizing -> connected
```

`disconnected` is terminal for one connection identity. Reconnect normally
creates a new `connection_id` and binds it to the same participant and surface.
A one-active-connection compatibility policy may replace an older connection,
but replacement changes no surface or interaction state.

| Transition | Authority | Durable fact |
|---|---|---|
| absent -> `connecting` | Browser/native client requests attachment; Tangent allocates the ID after minimum origin/session checks. | Insert `ConnectionRecord` with client kind, surface, attempted participant binding, safe transport metadata, and `connection.connecting`. |
| `connecting` -> `synchronized` | Tangent projection service after authorizing the binding and sending a snapshot at a durable surface/interaction revision. | Record synchronized revisions and time; append `connection.synchronized`. |
| `synchronized` -> `connected` | Tangent after client acknowledgement. | Record acknowledgement and lifecycle revision; append `connection.connected`. |
| `connected` -> `stale` | Tangent detects missed liveness, revision conflict, or an obsolete projection. | Record reason and observed revision; append `connection.stale`. |
| `stale` -> `resynchronizing` | Tangent or the bound client requests a fresh durable projection. | Record requested base/target revisions; append `connection.resynchronizing`. |
| `resynchronizing` -> `connected` | Tangent after the client acknowledges the current projection. | Record synchronized/acknowledged revisions; append `connection.connected`. |
| nonterminal -> `disconnected` | Tangent observes client close, transport loss, shutdown, revocation, or replacement. | Record time and typed reason; append `connection.disconnected`. No surface/interaction mutation is included in this transaction. |

Connection records are operational/audit facts, not correctness prerequisites
for resolving or retrieving an interaction. Sensitive headers, capability
material, and payload bodies are never stored in them.

### 8. Resolution and terminal-outcome delivery lifecycle

```text
queued -> delivering -> delivered -> acknowledged
             |
             +-> retryable-failure -> delivering

queued | delivering | retryable-failure -> terminal-failure
```

One terminal interaction outcome may have more than one destination/delivery
record. A resolved interaction uses `ResolutionDeliveryRecord`; `canceled`,
`expired`, `failed`, and `superseded` use `TerminalNotificationRecord`. Both
implement the lifecycle below. Delivery state cannot change the immutable
resolution or terminal interaction disposition.

| Transition | Authority | Durable fact |
|---|---|---|
| absent -> `queued` | Tangent terminalization service in the same transaction that records the resolution or other terminal disposition. | Insert `ResolutionDeliveryRecord` or `TerminalNotificationRecord` with its immutable outcome reference, destination binding, policy, and idempotency identity; append `delivery.queued`. |
| `queued` or `retryable-failure` -> `delivering` | Tangent delivery worker obtains a durable claim/lease. | Advance delivery revision and lease; append a `DeliveryAttempt` and `delivery.started`. |
| `delivering` -> `delivered` | The destination adapter records the strongest positive receipt its transport contract can actually observe. Merely returning a value from the application service is not sufficient. | Seal the typed adapter/transport receipt and delivered time; update delivery record; append `delivery.delivered`. This does not prove caller processing or business acceptance. |
| `delivering` -> `retryable-failure` | Transport adapter returns a retryable typed error, including loss of a waiting MCP response path. | Seal failed attempt, typed error, next eligibility, and revision; append `delivery.retryable_failed`. |
| `queued`, `delivering`, or `retryable-failure` -> `terminal-failure` | Tangent applies the pinned delivery policy or an authorized destination revokes the obligation. A delivered receipt cannot later become failure. | Record typed terminal reason, policy/actor, and revision; append `delivery.terminal_failed`. The interaction's terminal outcome remains unchanged. |
| `delivered` -> `acknowledged` | Calling application explicitly acknowledges the resolution/delivery identity. | Record caller receipt and time; append `delivery.acknowledged`. This still is not a business outcome. |

A later `Get` or `Await` call writes a separate `TerminalOutcomeRetrievalRecord`
for the same immutable terminal outcome. Repeated retrieval never creates a
new resolution or terminal notification. If its transport adapter can later
report a positive write or handoff receipt, that receipt may advance a
delivery record; an MCP handler return by itself records retrieval, not
delivery. A Tether receipt proves only Tether's transport contract, and an MCP
response write proves only the adapter boundary that Tangent can observe.

### 9. Drafts and downstream outcomes stay outside terminal-state shortcuts

`SaveDraft` requires an expected draft/interaction revision and creates an
immutable `DraftRevision`. Browser-local storage may cache a draft, but it is
not authoritative unless the interaction policy explicitly chooses browser
custody. Resolution records name the source draft revision when applicable.
Draft expiry or deletion uses a tombstone/event and never rewrites a
resolution.

A downstream owner may later report a typed outcome reference tied to the
resolution and its own object revision. Tangent stores that claim with its
source and assurance. It does not turn an `approved` resolution into a Torque
`done`, Hadron `advanced`, deployment `published`, or Nanite turn `complete`
transition.

### 10. Compatibility vocabulary and public tools

The public `tangent.session_*` tools, `roomID`, and `/r/<roomID>` URLs remain
supported compatibility vocabulary. This ADR does not rename or remove them.

| Existing surface | Compatibility mapping |
|---|---|
| `tangent.session_create` | Opens a caller-scoped surface. Existing `roomID` remains the alias of `surface_id`; a later additive response may also expose canonical handles. An additive optional idempotency key makes creation retry-safe within `caller_scope`; omission preserves today's create-every-call behavior and is explicitly not retry-deduplicated. |
| `tangent.session_advance` | Blocking adapter over `SubmitInteraction` plus `AwaitResolution`. Its single-active-advance behavior may remain an adapter policy; it is not a storage-model limit. |
| `tangent.session_get` | Compatibility projection over a surface and its interactions. Existing `status`, legacy `phase`, histories, and workflow projections remain readable. Canonical surface and interaction states must be added without reinterpreting `status` as both. |
| `tangent.session_advance_phase` | Compatibility operation for existing bundled flows. Phase names are presentation/package state or opaque external workflow correlation, not a generic Tangent business workflow lifecycle. |
| `tangent.session_set_phase_output` | Compatibility storage/projection for shipped workflows. New generic application logic must not depend on arbitrary phase blobs as interaction authority. |
| `tangent.session_close` | Maps to explicit `CloseSurface`. Its freeform `status` is preserved as a close reason/legacy value, not a downstream outcome or an implicit participant resolution. |
| `tangent.session_list` | Lists surface compatibility projections. `active_only` filters surface lifecycle, never browser-connection presence. |

Workflow-named tools such as `tangent.triage`, `tangent.form-collect`, the
writing tools, and `tangent.approval-queue` remain ergonomic adapters with
their current request and response shapes. They may create/reuse a surface,
submit one interaction, and block for a result, but must use the same durable
interaction service rather than their own queue or terminal lifecycle.
`tangent.approval-queue` remains the existing bounded batch workflow; it is not
redefined as the shared HITL inbox.

`go-envelopes.Response.Handle.WorkflowInstanceID` remains a compatibility field
for a genuine externally owned workflow instance. It is not Tangent's canonical
`interaction_id`, and its presence must not be used to infer a Hadron run. Until
the shared protocol gains explicit surface and interaction handles, adapters use
the existing URI or `ResourceID` where those fields fit and persist canonical
identities separately from the compatibility response.

Two current behaviors are intentionally not compatibility promises:

- active WebSocket loss will no longer close the room/surface or terminally
  fail its interaction with `ROOM_DISCONNECTED`; it disconnects a connection
  and may fail only a live delivery/wait attempt;
- process restart will no longer turn every pending interaction into a
  `SERVER_RESTART` timeout. Recovery follows durable interaction policy.

Participant cancel, caller cancel/withdrawal, expiry, transport cancellation,
connection loss, surface close, and process shutdown must remain distinguishable
in canonical records even where a legacy adapter has a smaller response enum.
The existing wire value `ResponseStatusCancelled` (`"cancelled"`) remains
unchanged for compatibility; adapters map it to or from the canonical
interaction state `canceled` plus its separately stored typed origin.

The internal Go package name `room`, the `Room` type, and SQLite table/column
names are implementation details and may remain during migration. New domain
and application interfaces use canonical names so a later refactor is
mechanical rather than a second semantic change.

### 11. Migration sequence and compatibility projection

Implementation is additive and incremental:

1. Add the durable canonical records and typed application-service operations
   behind current transports. Keep HTTP, MCP, WebSocket, and future desktop
   adapters outside the domain/application packages.
2. Preserve every existing `rooms.id` as the corresponding `surface_id`.
   Backfill lifecycle from explicit room close facts only; do not infer closure
   from browser or process history.
3. Project existing envelope rows into legacy interactions without inventing
   identity assurance, definition provenance, cancellation authority, or
   business outcomes that the old schema did not record. Preserve the original
   row and status in compatibility metadata.
4. Where a known built-in definition and request snapshot make an old pending
   row recoverable, migrate it as a staged legacy interaction. Otherwise expose
   a typed legacy-recovery failure; never label it a participant cancellation
   or business rejection. Existing timeout/error rows remain predictably
   readable through the compatibility projection.
5. Route one workflow adapter at a time through the canonical service while
   retaining tool names, `roomID`, routes, schemas, response payloads, persisted
   history, and phase projections. Contract and end-to-end tests guard both
   canonical records and legacy views.
6. Retire process-local pending channels, disconnect-driven room closure, and
   stale-pending startup timeouts only after the durable path owns resolution
   and delivery correctness.
7. Any removal of `session_*`, `roomID`, `/r/<roomID>`, or legacy projections
   requires a separately accepted ADR, a versioned deprecation period, usage
   evidence, and explicit migration tooling.

Schema details, SQL table layout, retry intervals, retention durations, and
authentication/capability mechanisms are delegated to their implementation or
security decisions. They may not collapse the boundaries locked here.

## Consequences

### Positive

- Human work can outlive an MCP request, browser tab, agent turn, or Tangent
  process without losing a valid resolution.
- A reconnecting or second client can synchronize from durable revisions
  without inheriting the identity of the old socket.
- Tangent can accurately report resolved-but-undelivered and
  delivered-but-not-acknowledged states.
- Nanite, Tether, Torque, and Hadron retain their own identities and business
  authority while composing through opaque references and typed receipts.
- Existing tools and URLs remain usable while their implementation migrates
  incrementally.
- A shared `/hitl` surface can be operator-scoped without introducing a second
  queue-only lifecycle.

### Negative and migration risks

- Domain and compatibility vocabulary will coexist for a meaningful period;
  docs, logs, and code review must distinguish `roomID` aliases from canonical
  surface identity.
- Legacy rows do not contain enough provenance to retroactively claim
  authenticated actors or exact transition causes. Migration must preserve
  uncertainty rather than invent clean history.
- Callers that currently depend on browser disconnect or process restart to
  terminate a blocking call will observe longer-lived pending interactions.
  Explicit cancellation, expiry, and handle retrieval must replace that
  accidental behavior.
- A durable delivery outbox, compare-and-set transitions, idempotency indexes,
  and revisioned projections add storage and concurrency complexity.
- Persisting connection audit and draft revisions adds retention/redaction
  obligations. Safe metadata and per-definition custody policy are required.
- The loopback-only first slice still lacks authenticated caller and participant
  identity. The ADR makes that limitation visible but does not solve it.
- Current workflow phase blobs mix presentation, draft, and business-shaped
  data. Compatibility projections must remain isolated so they do not become
  the new interaction model by inertia.

## Alternatives considered

### Keep `Room` as the single aggregate and add more statuses

Rejected. A composite status cannot truthfully represent surface, interaction,
connection, and delivery at once. More flags would preserve the same lifetime
and authority bugs.

### Make a browser tab or MCP request the interaction owner

Rejected. Both are disposable transport/client instances. Either choice loses
or cancels valid human work during normal navigation, retry, timeout, or
restart.

### Break and rename `session_*` and `roomID` immediately

Rejected. Existing agents, skills, workflows, URLs, stored rows, and manual
recipes depend on them. Compatibility aliases let semantics change safely
before public vocabulary is reconsidered.

### Add the HITL inbox as an independent durable queue

Rejected. A queue-specific lifecycle would duplicate idempotency, interaction
state, resolution immutability, delivery, and recovery. The inbox is a durable
surface and projection over the same substrate.

### Let Tether, Torque, Hadron, or Nanite identity become Tangent's primary key

Rejected. Standalone Tangent would stop working and lifecycle ownership would
leak across products. External identities remain typed correlations; trusted
adapters may strengthen their assurance without replacing Tangent IDs.

### Treat a transport receipt as downstream acceptance

Rejected. A transport can prove only its delivery contract. The calling system
must validate freshness and policy, execute its own transition, and optionally
return a separate outcome receipt.

## Review questions

Chrispian accepted this ADR as drafted on 2026-09-03. Dependent tasks may treat
the following material choices as locked:

1. `roomID`, `/r/<roomID>`, and `tangent.session_*` remain compatibility
   vocabulary through an additive migration rather than being renamed now.
2. Direct loopback MCP identity remains explicitly asserted; idempotency uses
   an explicit caller application scope, with `standalone-local` only as the
   legacy fallback, and never uses an MCP session as identity.
3. Browser/MCP/process loss ceases to be cancellation authority. Existing
   `ROOM_DISCONNECTED` and startup-timeout behavior are intentionally retired
   as the durable service lands.
4. The shared operator HITL inbox is modeled as an operator-scoped Surface, not
   as a second queue domain or a special kind of Tangent session.
5. Only an interaction presented at its exact pinned request, definition, and
   projection revisions is respondable; terminalization and its first durable
   delivery/notification obligation commit atomically.
6. Caller `withdraw` is a distinct typed cause of canonical `canceled`, while
   participant cancel, expiry, and transport cancellation retain different
   durable meanings.

The review disposition was: accept this decision as drafted.

## References

- [`../interactive-collaboration-direction.md`](../interactive-collaboration-direction.md)
- [`../architecture.md`](../architecture.md)
- [`../mcp-integration.md`](../mcp-integration.md)
- [`../../internal/room/room.go`](../../internal/room/room.go)
- [`../../internal/room/store.go`](../../internal/room/store.go)
- [`../../internal/mcp/session_tools.go`](../../internal/mcp/session_tools.go)
- [`../../internal/ws/handler.go`](../../internal/ws/handler.go)
- `github.com/hollis-labs/go-envelopes/types.go`
- Nanite `README.md` (agent/application session ownership)
- Tether `README.md`, ADR 0023 (message delivery versus consumption), and ADR
  0030 (long-lived runtime-session lifecycle)
- Torque `README.md` and ADR 0001 (durable task/work authority)
- Hadron ADR 0001 (daemon-owned orchestration/persistence) and ADR 0006
  (workflow-engine boundary)
- Torque project `PRJ-20260825-0002`, sprint `SP-20260825-0002`, and task
  `CW-20260825-0057`
- HITL execution plan `CW-20260904-0008`
