# Tangent Interactive Collaboration Direction

**Status:** Directional architecture draft

**Date:** 2026-08-22
**Scope:** Desired product boundaries and architecture; not an implementation plan

## Purpose

This document describes the intended place of Tangent in the Hollis Labs
portfolio. It evaluates Tangent's interaction model, rooms, envelopes,
renderers, human input, persistence, transports, local process model,
extensions, trust boundaries, and composition with other portfolio tools.

It deliberately does not define phases, estimates, task breakdowns, migration
steps, release sequencing, or compatibility work. Existing documentation and
code remain the current implementation truth until explicitly superseded.
This document defines a target direction for a later architecture and planning
session.

## Portfolio axiom

> Hollis tools own execution and operational state, but not the business
> definitions or business data they operate on.

For Tangent, that means:

- The calling application owns the purpose of an interaction and the business
  object to which it refers.
- The envelope publisher owns the interaction type, schemas, semantic meaning,
  presentation contract, and interpretation of its possible responses.
- The participant owns the meaning of the input or decision they provide.
- The business system owns whether that input constitutes approval,
  acceptance, publication, task completion, or any other domain transition.
- `go-envelopes` owns the portable protocol vocabulary and common interaction
  contracts, not every application's business workflow.
- Credential authorities own secrets and their lifecycle.
- Tangent owns the operational interaction record: definition resolution,
  validation, presentation, room and connection state, drafts placed under its
  custody, typed resolution capture, response delivery attempts, and audit.

Persisting an envelope or response does not transfer ownership of the source
document, diff, task, workflow, fragment, workspace, or agent session to
Tangent. Tangent is authoritative for what it presented and what response it
captured, not for the external business consequence of that response.

The useful shorthand is:

> Tangent turns publisher-defined interaction contracts into safe, durable,
> app-sized human-agent collaboration surfaces and typed resolutions.

## Product definition

Tangent is the local-first, agent-summoned interactive surface for work that
does not fit naturally in chat. It is Mode 1 of the broader Hollis Labs
interactive collaboration model: a separate-window, app-sized surface.

Tangent:

1. Accepts a versioned interaction request from an authorized caller.
2. Resolves and validates the publisher-owned interaction definition.
3. Creates or reuses a durable surface without claiming the caller's session.
4. Presents the request through an appropriate trusted or sandboxed renderer.
5. Captures participant drafts and a typed terminal response under declared
   custody and retention policy.
6. Produces an immutable resolution record and delivers it to the caller.
7. Exposes the same interaction semantics through portable MCP and optional
   portfolio-native transports.
8. Provides local operational visibility into active, suspended, resolved,
   expired, and failed interactions.

Tangent is not a workflow engine, task manager, agent runtime, conversation
host, general messaging system, content inbox, knowledge authority, document
editor of record, spreadsheet engine, infrastructure control plane, secret
manager, or plugin marketplace.

Tangent can render a workflow step without owning the workflow. It can render
an approval queue without owning approval policy. It can render a diff without
owning the repository. It can capture a form without owning the submitted
business record.

## Scope boundary: Mode 1 only

The separate-window constraint is a product boundary, not a current UI
limitation.

Tangent owns:

- app-sized, separately addressable interaction surfaces
- persistent rooms and reopenable interaction state
- richer layouts than a chat turn can reasonably contain
- focused review, comparison, annotation, form, canvas, and guided-input
  experiences
- explicit participant submission, cancellation, and resolution

Tangent does not absorb:

- Nanite's inline chat controls or agent-session UI
- fast ephemeral popovers whose lifecycle is the current chat turn
- general spatial desktop or canvas orchestration
- chat multiplexing across agents and conversations
- application-native screens whose data authority belongs to that application

Sharing envelope schemas or renderer components across these modes is useful.
Sharing a protocol does not require one runtime to own every presentation mode.

## Architecture sketch

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

Transport connections, browser windows, Tangent surfaces, interaction
instances, agent sessions, workflow runs, and business objects are distinct
identities even when a simple deployment maps some of them one-to-one.

## Responsibility boundaries

| Concern | Authoritative owner | Tangent responsibility |
|---|---|---|
| Interaction purpose and business object | Calling application | Preserve typed references and correlation; do not reinterpret |
| Interaction definition | Envelope publisher | Resolve, validate, materialize, cache, and report exact revision |
| Shared envelope protocol | `go-envelopes` and its publishers | Implement the host side consistently |
| Renderer implementation | Definition publisher, subject to Tangent trust policy | Load, isolate, execute, and observe |
| Surface and room lifecycle | Tangent | Create, reopen, suspend, close, expire, and audit |
| Participant identity | Identity authority or local user session | Authenticate or bind a verified principal and capability |
| Participant draft | Participant, with declared custodian | Persist only under the interaction's custody and retention policy |
| Typed resolution | Participant for input; Tangent for capture fact | Validate, timestamp, seal, persist, and deliver |
| Business acceptance | Calling application | Preserve downstream receipt or outcome reference when supplied |
| Workflow run and gates | Hadron or another workflow owner | Render a gate and return evidence |
| Task and work acceptance | Torque | Render review surfaces and return evidence |
| Agent session and conversation | Nanite, Tether, Torque, Hadron, or another caller | Correlate by opaque reference only |
| Messaging and gateway session | Tether when composed | Consume transport and identity bindings without taking ownership |
| Fragment/content triage | Fragments Engine or another content authority | Render the interaction; do not own content disposition |
| Knowledge and memory | Tesseract namespace authority | Explicitly project a pointer or selected resolution when requested |
| Workspace and repository | Cerberus/Coder and source-control authority | Render scoped artifacts and return selections or annotations |
| Secrets | External credential authority | Accept references or narrow grants; never become the credential store |
| Local service supervision | OS service manager, optionally materialized by Cerberus | Expose lifecycle, health, readiness, and graceful shutdown |

## Authority and custody model

### Interaction definition

An interaction definition is publisher-owned, versioned input:

```text
InteractionDefinition
  namespaced kind and immutable version
  publisher identity and provenance
  request and response schemas
  renderer reference and renderer trust class
  supported actions and effects
  accessibility and presentation capabilities
  attachment and artifact contracts
  sensitivity and default retention policy
  compatible host and protocol versions
  digest and signature or trust evidence
```

Tangent may maintain a registry and materialized cache, but registration does
not transfer definition ownership. Every interaction binds the exact resolved
definition revision and digest. Updating a definition creates a new revision;
it does not reinterpret historical interactions.

Generic, broadly reusable interaction kinds may live in `go-envelopes`.
Application-specific business types stay with their publisher. The shared
catalog must not become a dumping ground for every product's workflow logic.

### Interaction request

The caller creates an immutable request instance:

```text
InteractionRequest
  interaction identity and idempotency key
  definition reference and expected digest
  caller principal and application identity
  external session, run, task, and object references
  payload or verified artifact references
  presentation hints
  requested participant role or audience
  sensitivity, custody, and retention overrides
  expiry and cancellation policy
  causation, correlation, and trace context
```

The request captures what Tangent was asked to present. It is not a mutable
copy of the business object. If the source object changes, the caller submits a
new request, supersedes the old one, or supplies a versioned live-data
capability whose freshness rules are explicit.

### Surface and room

A Tangent surface is a durable presentation container. A room is its
operational collaboration context:

```text
Surface
  stable Tangent identity
  caller and owning scope
  title and presentation metadata
  participant bindings and capabilities
  ordered interaction instances
  focused interaction or view
  lifecycle and retention policy
  creation, activity, suspension, and closure facts
```

A room is not an agent session, Hadron run, Torque task, Tether conversation,
browser tab, WebSocket connection, or Coder Workspace. It may correlate with
all of them by opaque reference.

The browser window is a projection of the surface. Closing a tab changes a
connection state; it does not necessarily cancel the interaction, close the
room, or revoke the caller's request. Those transitions require explicit
policy or commands.

### Draft

A draft is participant work that has not been submitted as a resolution:

```text
InteractionDraft
  interaction and participant identity
  definition version
  monotonically increasing revision
  schema-shaped draft data or external artifact refs
  saved time and client instance
  sensitivity and expiry
```

The interaction definition declares whether drafts are disabled, ephemeral,
browser-local, Tangent-custodied, or externally custodied. Browser local
storage is a cache or recovery aid, never an implicit authoritative store.
Sensitive interactions can prohibit client persistence entirely.

### Resolution record

A response becomes a durable resolution only after schema validation and an
authorized terminal action:

```text
ResolutionRecord
  interaction and definition identity
  participant principal and asserted role
  response kind and schema-shaped payload
  source draft revision, if any
  submitted, validated, and recorded times
  terminal disposition
  referenced artifact revisions
  integrity digest
  causation, correlation, and trace context
```

Tangent is authoritative that this participant submitted this response to this
interaction definition and request revision. The calling application remains
authoritative for what happened next.

An approval-looking response is not automatically an approval of a Torque
task, Hadron gate, deployment, document, payment, or infrastructure change.
The external owner authenticates the actor requirements, verifies freshness,
applies policy, performs the domain transition, and returns an outcome or
receipt if the composition requires it.

### Delivery and downstream outcome

Resolution capture and result delivery are separate facts:

```text
ResolutionDelivery
  resolution identity
  destination binding
  attempt and idempotency identity
  attempt times and result
  transport receipt
  retry or terminal disposition

DownstreamOutcome
  resolution identity
  external system and object reference
  accepted, rejected, superseded, or ignored
  observed time and external revision
```

Tangent can truthfully report `resolved but not delivered`, `delivered but not
acknowledged`, or `acknowledged but rejected by the business owner`. A closed
MCP request must not erase a valid human resolution.

## Core domain model

### Definition registry and materialization

Tangent's registry resolves definition references from built-in packages,
configured registrations, portfolio plugins, or approved external extension
sources. Resolution produces an immutable materialization containing the
schemas, renderer assets, declared effects, and trust evidence used for an
interaction.

The registry owns lookup and compatibility facts. The publisher remains the
source of truth for definition content. Cached definitions retain provenance,
digest, last verification, and eviction policy.

Definition availability has distinct states:

```text
registered -> resolved -> verified -> materialized -> available
                                      \-> incompatible
                                      \-> quarantined
                                      \-> unavailable
```

`Registered` does not imply that the renderer can load, its dependencies are
present, or Tangent can safely execute it.

### Interaction instance

An interaction instance binds request, definition, surface, audience, and
runtime state. Its lifecycle is independent from the surface lifecycle and
from result delivery.

Tangent supports ordered and related interaction instances in one surface.
The host may focus one interaction at a time without treating “one pending
envelope per room” as a foundational data-model restriction. Policies can
still serialize terminal decisions where overlapping responses would be
ambiguous.

### Participant and connection

Participants and connections are separate:

```text
ParticipantBinding
  principal and identity authority
  surface role
  view, draft, resolve, cancel, or administer capabilities
  binding creation and expiry

ClientConnection
  connection identity
  participant binding
  browser, desktop, or native client kind
  connected and last-seen times
  focused interaction and projection revision
```

Several connections can represent one participant. Several participants can
observe a surface while exactly one role is permitted to resolve an
interaction. Replacement, takeover, reconnect, and conflict policies are
explicit rather than consequences of a single WebSocket pointer.

### Artifact reference

Large or sensitive payloads use typed artifact references rather than
unbounded inline JSON:

```text
ArtifactRef
  authority and stable identity
  immutable revision or digest
  media and logical kind
  size and sensitivity
  scoped retrieval capability reference
  expiry and retention contract
  optional safe preview reference
```

Tangent may cache a rendered projection under policy. It does not silently
adopt the source artifact or grant the caller access it did not already have.

### Audit event

Domain audit is an append-only record distinct from application logs:

```text
InteractionEvent
  event identity and type
  surface, interaction, definition, and resolution references
  actor and authority
  prior and resulting lifecycle revisions
  causation, correlation, and trace context
  safe metadata
  recorded time
```

Events support reconstruction of operational decisions without logging
arbitrary payload bodies.

## Lifecycle separation

One broad `active`, `pending`, or `closed` field cannot truthfully describe the
system.

### Surface lifecycle

```text
created -> active <-> suspended -> closed
   |          |                     |
   +----------+---------------------+-> expired
```

A surface can be active with no unresolved interaction. A disconnected client
does not necessarily suspend or close it.

### Interaction lifecycle

```text
submitted -> validated -> staged -> presented -> in-progress -> resolved
     |           |          |           |             |          |
     +-----------+----------+-----------+-------------+-> canceled
     +-----------+----------+-----------+-------------+-> expired
                 +----------+--------------------------> failed
                             +--------------------------> superseded
```

These states answer different questions:

- `validated`: the request conforms to the pinned definition.
- `staged`: the interaction is durably available to its surface.
- `presented`: an authorized client acknowledged a renderable projection.
- `in-progress`: participant work or draft state exists.
- `resolved`: an authorized terminal response was recorded.
- `superseded`: a newer request replaced this request without rewriting it.

`Presented`, `respondable`, and `resolved` are not synonyms.

### Connection lifecycle

```text
connecting -> synchronized -> connected -> disconnected
                       \-> stale -> resynchronizing
```

Connection loss affects live projection delivery. It does not define the
interaction's terminal state.

### Resolution delivery lifecycle

```text
queued -> delivering -> delivered -> acknowledged
  |           |             |
  +-----------+-------------+-> retryable-failure
  +-----------+-------------+-> terminal-failure
```

This prevents a transport timeout from being mistaken for participant
cancellation or a business rejection.

### Downstream business lifecycle

The caller owns any subsequent state machine. Tangent stores only a typed
receipt or outcome reference when one is returned. It does not mirror the
caller's entire task, workflow, deployment, or document lifecycle.

## Interaction contract

### Envelope protocol

The envelope is the portable interaction request and response shape. The
protocol should standardize:

- identity, version, type, title, data, metadata, and trace context
- schema discovery and compatibility
- typed response and error vocabulary
- artifact and attachment references
- presentation capabilities and graceful fallback
- cancellation, expiry, supersession, and idempotency
- sensitivity and persistence hints

It should not standardize every application's workflow phase, domain status,
approval policy, or business data model.

### Definition versus instance

Schemas and renderer metadata belong to the immutable definition. Business
data and caller references belong to the instance. Tangent operational state
belongs to the host. Participant input belongs in draft or resolution records.

Keeping these four layers separate prevents a mutable room JSON blob from
becoming the accidental owner of every workflow's data.

### Generic and ergonomic operations

The stable application service centers on a small generic vocabulary:

```text
ListInteractionKinds
ResolveInteractionDefinition
OpenSurface
SubmitInteraction
GetSurface
GetInteraction
SaveDraft
ResolveInteraction
CancelInteraction
SupersedeInteraction
CloseSurface
AwaitResolution
```

Workflow-named MCP tools can remain useful ergonomic adapters. They should be
generated or registered projections of the same interaction contract, not
independent implementations with separate persistence and lifecycle meaning.

### Synchronous and asynchronous callers

Human interaction can outlive an MCP HTTP request, agent turn, transport
session, browser connection, or process restart. The authoritative contract is
handle-based and asynchronous:

1. Submit or open returns a stable surface and interaction handle.
2. The caller may inspect, await, subscribe, cancel, or resume by handle.
3. Tangent records a resolution independently of whether the original caller
   is still connected.
4. Delivery is retried or later retrieved according to policy.

A blocking `call -> wait for browser -> return response` flow is a convenient
compatibility adapter over this model. It is not the durability boundary.

The caller supplies an idempotency key so transport retries cannot create
duplicate rooms or duplicate business decisions.

### Live updates

Progress panels, dashboards, previews, and other evolving surfaces use
versioned interaction updates:

- updates bind the interaction identity and prior revision
- snapshot replacement and append-only events are different operations
- stale updates are rejected or explicitly rebased
- terminal resolution does not silently reopen on a later update
- presentation-only state remains distinct from source data

Tangent does not poll arbitrary business systems or become their reporting
store. The publisher pushes a new snapshot, provides a scoped live-data
adapter, or sends an artifact reference.

## Workflow boundary

Tangent supports multi-turn interaction without becoming a workflow engine.

It may own presentation navigation such as:

- which pane, item, tab, or wizard step is currently visible
- draft completion within one interaction definition
- renderer-local validation and progressive disclosure
- an ordered history of interaction instances in a room

It does not own:

- a reusable workflow language
- cross-application branching or orchestration
- business phase definitions
- retry, compensation, or scheduling for domain work
- task dependencies or completion policy
- whether a response advances an external run

A publisher can define a compound interaction, including a wizard, when the
steps form one bounded user submission. A durable business process spanning
multiple effects and systems belongs in Hadron or the appropriate domain
application. Tangent receives one interaction at a time and returns typed
evidence.

Room-level `current_phase`, `phases_visited`, and arbitrary phase outputs are
therefore not part of Tangent's target core domain. Compatibility projections
may remain, but future semantics should be publisher-owned interaction state
or an opaque external workflow reference. A writing flow, approval process, or
deployment review does not become Tangent-owned merely because Tangent renders
several of its steps.

## Renderer and extension model

### Renderer registry

The renderer host maps a definition to a compatible presentation component.
Resolution is deterministic and observable:

```text
definition kind + version
  -> compatible renderer registration
  -> trust and capability evaluation
  -> asset materialization
  -> renderer instance
  -> SurfaceProjection
```

If a rich renderer is unavailable, Tangent may use a declared safe fallback
only when the definition says the fallback preserves the interaction's
meaning. It must not silently downgrade a diff approval to an unstructured
text box or discard required evidence.

### Plugin contract

The Hollis Labs plugin SDK is the preferred extension mechanism for
third-party or portfolio interaction packages. A package declares:

- stable plugin and publisher identity
- interaction kinds and immutable versions
- request and response schemas
- renderer assets or declarative renderer references
- required host, protocol, and plugin SDK versions
- requested browser, filesystem, network, clipboard, export, and process
  capabilities
- sensitivity, persistence, and telemetry behavior
- configuration schema containing no secret values
- health, compatibility, and diagnostics

Tangent owns registration, validation, capability negotiation, lifecycle, and
isolation. The plugin owns its definition and behavior. Tangent may cache the
materialized package and its history for reproducibility; it does not rewrite
or become the source of the plugin's authored content.

### Trust classes

Not all renderers receive the same authority:

- **Core trusted:** shipped and reviewed with the Tangent release.
- **Portfolio trusted:** signed Hollis Labs package with declared capabilities.
- **Declarative:** schema-driven UI rendered by trusted Tangent primitives.
- **Sandboxed code:** isolated extension with no ambient host authority.
- **External surface:** linked application owns rendering and returns a signed
  result through a narrow handoff.

Untrusted JavaScript cannot execute in Tangent's main application origin or
inherit browser storage, MCP access, filesystem handles, cookies, clipboard,
or network access. Capability escalation is explicit and visible.

### `go-envelopes`, the plugin SDK, and Sigil

These tools have complementary roles:

- `go-envelopes` defines the portable wire model and reusable schema catalog.
- The plugin SDK defines how a package is registered, configured, granted
  capabilities, initialized, observed, and stopped.
- Tangent hosts and renders interaction instances at runtime.
- Sigil may generate schemas, bindings, renderer scaffolds, or registry code at
  build time; it is not Tangent's runtime renderer or plugin authority.

Generated Go and TypeScript artifacts must carry source definition digests so
schema drift is detectable rather than inferred from a successful build.

### Bundled workflows

Triage, feedback, form collection, design iteration, whiteboard, spreadsheet
review, dashboards, diff review, file picking, progress panels, wizards,
approval queues, and writing interactions are valuable prior art and should be
preserved as reusable interaction packages.

Their presence in the distribution does not make their business semantics
part of Tangent core. A bundled package can be maintained beside the host,
published by another Hollis application, or promoted into a generic envelope
catalog according to how reusable its semantics actually are.

Fast-Triage's useful interaction patterns can remain available through
Tangent. General capture and content triage authority still belongs to the
content system invoking those patterns.

## Presentation safety

### Untrusted content

HTML, Markdown, SVG, images, diffs, document previews, spreadsheet cells,
filenames, model output, and plugin assets are untrusted display content.
Rendering policy includes:

- escaping and sanitization appropriate to each content type
- a restrictive content security policy
- isolated origins or sandboxed frames for active content
- no same-origin privilege for untrusted HTML
- no network, navigation, forms, popups, downloads, or script execution unless
  the definition declares and policy grants it
- bounded payload, DOM, image, archive, and decompression sizes
- safe link handling and explicit external navigation
- clipboard and export actions requiring declared capability and visible user
  intent
- structured `postMessage` channels with origin, source, schema, and nonce
  validation

The current design-iteration sandbox is strong prior art: active preview
content runs without same-origin privilege and with network and navigation
blocked. The target generalizes that trust model across all extension
renderers.

### Filesystem and workspace access

A text path is not a capability. File picking, diff review, export, and
attachment workflows operate on scoped handles or declared roots:

- the caller or workspace authority grants the available scope
- Tangent resolves only within that scope
- path traversal, symlink escape, case normalization, and race-safe open
  semantics are enforced
- selection does not imply read, write, execute, or upload authority
- returned references include authority, revision or digest, and granted use
- content access expires independently from the room

When composed with a Cerberus-managed Coder Workspace, the Workspace authority
owns its filesystem and access policy. Tangent receives a scoped artifact or
workspace handle; it does not discover or manage Coder workspaces.

### Actions and effects

Renderer actions are either:

- pure presentation changes
- draft mutations
- terminal response submissions
- explicit host-mediated effects

An arbitrary action identifier does not authorize a process, network request,
file write, deployment, or external API call. Host-mediated effects require a
declared capability, authorized participant intent, idempotency, audit, and a
typed receipt. Business effects generally return to the caller to execute.

## Authentication, authorization, secrets, and privacy

### Localhost is not identity

Loopback binding is a valuable exposure limit, but it is not authentication.
Other local processes and browser contexts may reach a localhost port. Room
UUIDs are locators, not sufficient authorization credentials.

The target distinguishes:

- caller application identity
- caller user or agent principal
- participant human identity
- client connection identity
- plugin publisher identity
- capability grants

Each operation checks the relevant principal and scope. A caller authorized to
open a surface is not automatically authorized to read all rooms, resolve an
interaction, close another caller's surface, or load sensitive history.

### Room invitations and browser sessions

A room URL names a surface. Access uses a separate, scoped, expiring capability
or authenticated browser session. View, draft, resolve, cancel, and administer
are distinct permissions.

Capability material should not appear in normal logs, analytics, page titles,
referrer headers, or persisted room metadata. One-time launch handoffs, URL
fragments exchanged for secure local sessions, or equivalent mechanisms are
preferable to treating the path itself as a bearer secret.

### Secret boundary

Tangent does not own provider credentials, API keys, SSH keys, OAuth refresh
tokens, workspace secrets, or application secrets.

Configuration stores credential references and non-secret settings. At
runtime, Tangent may consume a narrowly scoped, short-lived grant when a
host-mediated effect truly requires one. It does not expose that grant to an
envelope payload, renderer, browser storage, plugin config, audit event, or
telemetry stream.

Tangent can provide plumbing that lets Cerberus, a build pipeline, the OS, or a
credential broker materialize a runtime grant. Plumbing is not custody.

### Sensitivity and retention

Interactions can contain source code, unpublished writing, screenshots,
personal data, operational logs, deployment plans, and other sensitive
material. Every definition and instance has an effective policy covering:

- inline storage versus external reference
- draft persistence and client-side caching
- resolution retention
- audit metadata
- telemetry redaction
- export and clipboard permission
- allowed renderer and plugin trust class
- automatic expiry and user deletion
- backup inclusion
- model or network egress

Suggested custody classes are:

- `ephemeral`: memory only; no history or client cache
- `interaction`: retained until terminal state plus a short recovery window
- `surface`: retained with the room until explicit closure or expiry
- `durable-record`: retained as an immutable interaction record
- `external-reference`: Tangent retains identifiers and digests, not content

The caller can request stricter handling than the definition default. A plugin
cannot silently weaken host or user policy.

## Persistence, concurrency, and recovery

### Authoritative state

Tangent's durable store contains only state for which Tangent has operational
authority:

- surfaces and their lifecycle
- interaction instances and pinned definition bindings
- participant bindings and safe identity references
- drafts when Tangent is the declared custodian
- immutable resolutions
- resolution delivery attempts and receipts
- registered definition materializations and compatibility facts
- audit events
- retention and deletion records

Source documents, repositories, tasks, workflow runs, messages, and knowledge
remain external references unless an interaction explicitly grants Tangent a
bounded snapshot custody mode.

### Transaction boundaries

Operations that establish a durable fact commit it atomically with their
outbox or follow-on obligation:

- submitting an interaction commits its definition binding and presentation
  obligation
- saving a draft commits one monotonic revision
- resolving commits the immutable response and delivery obligation
- canceling or superseding commits the terminal state and notification
- closing a surface commits its policy-defined interaction disposition

Live WebSocket sends, MCP replies, browser acknowledgements, and external
callbacks happen after commit and can be retried idempotently.

### Concurrency

Concurrency policy is expressed in the domain rather than through one in-memory
mutex:

- optimistic revisions protect drafts and projections
- terminal resolution uses a compare-and-set transition
- participant roles determine who can resolve
- optional resolver leases serialize interactions that require it
- duplicate requests collapse through idempotency keys
- several browser connections can resynchronize from durable revision state
- one process or a coordinated store authority owns claims and outbox delivery

Exactly one active WebSocket and one pending envelope per room may remain a
supported policy profile. They are not assumed by the storage or identity
model.

### Restart and reconnect

After restart, Tangent can distinguish and recover:

- staged interactions never presented
- presented interactions with no saved draft
- draft-bearing interactions awaiting submission
- resolved interactions awaiting caller delivery
- delivered interactions awaiting optional business acknowledgement
- expired or revoked capabilities

A process restart does not automatically turn every pending interaction into a
timeout. Recovery follows the request's expiry and cancellation policy.

Clients synchronize by durable surface and interaction revision. Reconnect
does not depend on replaying an in-memory channel or treating the latest socket
as the room identity.

### SQLite and larger deployments

SQLite remains a strong local-first store when one authoritative Tangent
runtime owns migrations, claims, and writes. WAL, connection limits,
transaction discipline, backup, and repair are part of that deployment
contract.

A shared or multi-host deployment can use another transactional store without
changing the interaction model. Horizontal processes coordinate through
durable claims and event delivery, not process-local room maps.

### Backup, restore, export, and deletion

Backup covers Tangent-owned operational state and explicitly included cached
artifacts. Restore preserves definition digests, interaction identities,
terminal resolutions, audit history, and pending delivery obligations.

Restore never blindly repeats a terminal external effect. Idempotency and
downstream receipts determine reconciliation.

Deletion distinguishes:

- expiring a capability
- deleting a draft
- redacting retained payload content while preserving a minimal audit fact
- closing a surface
- removing a cached definition or artifact
- honoring deletion in an external authority

Tangent does not claim to delete source data owned by another system.

## Interfaces and transport parity

### Shared application service

MCP, native Nanite transport, HTTP, desktop bindings, WebSocket projection,
and CLI administration adapt one application service. They do not implement
parallel business rules.

Every interface preserves:

- caller and participant identity
- definition and instance revisions
- idempotency
- lifecycle semantics
- typed errors
- sensitivity and authorization
- causation, correlation, and trace context

### MCP

MCP is the portable lowest-common-denominator for discovery and invocation.
Tangent uses the official SDK where it covers the protocol and avoids a custom
transport implementation.

The generic interaction operations are the stable core. Registered packages
may advertise ergonomic workflow tools. Legacy SSE can remain a compatibility
transport, but Streamable HTTP is the primary standards-aligned surface.

An MCP session is not a Tangent room. Stateless discovery, stateful transport,
and durable interaction handles can coexist without sharing identities.

### Nanite-native channel

When Tangent is composed as a managed child of Nanite, a native side channel
can provide lower-latency event injection, mid-turn updates, lifecycle
correlation, and richer capability negotiation. It speaks the same interaction
contract and does not create a second definition, renderer, or persistence
model.

Nanite remains the agent runtime and session authority. Tangent remains the
separate-window surface.

### Browser and desktop shell

WebSocket or another live channel carries revisioned surface projections and
participant commands. It is not the source of truth.

A browser and a Wails shell are presentation hosts over the same application
service. Wails may package the daemon and shell together, but the logical
authority remains singular. A desktop wrapper must not create a second room
store, plugin registry, or interaction implementation.

### CLI

The CLI is primarily an administrative and diagnostic client for the running
runtime. Migrations, repair, backup, restore, definition validation, and export
are one-off processes from the same release with explicit maintenance fencing.

Direct CLI access must not create an uncoordinated second writer beside the
daemon.

## Local runtime and service model

Tangent has one authoritative long-running host when persistent rooms,
reconnect, and agent invocation are enabled. That host owns:

- the listener and live transports
- the database connection and migrations
- interaction claims and terminal transitions
- definition and renderer registrations
- delivery outbox processing
- health, readiness, and graceful shutdown

Tangent does not self-daemonize, maintain its own PID files, or implement an OS
service manager. Supported shapes include:

- a foreground development or one-off process
- a user desktop application whose lifecycle owns the embedded host
- an OS-managed user service with a browser or Wails client
- a Cerberus-materialized service registration supervised by launchd, systemd,
  a container runtime, or another declared platform owner

Cerberus can validate configuration, materialize service definitions, connect
attached resources, and observe health. The OS or runtime owns daemon
supervision. Tangent owns application-level draining and recovery.

Liveness, readiness, and capability availability are different:

- liveness: the process can respond
- readiness: the store, migrations, registries, and core renderer host are
  usable
- capability availability: a particular interaction kind and its declared
  dependencies are materialized and healthy

A process listening on a port is not necessarily ready to accept an
interaction.

## Configuration and definition ownership

Configuration is external and non-secret. It binds:

- listener and local runtime settings
- state, cache, and artifact paths
- effective retention and security policy
- registered interaction package references
- trusted publisher roots
- attached credential broker or identity authority references
- transport enablement
- observability endpoints and redaction policy

Authored interaction definitions remain in their publisher-controlled
repositories or registries. Tangent may validate and materialize them but does
not become their authoring source of truth.

Effective configuration is immutable for a process generation. A reload
creates a new validated snapshot with provenance and diagnostics. Active
interactions remain bound to the definition and policy revisions under which
they were created unless an explicit security revocation overrides them.

Configuration precedence is deterministic and inspectable. Environment
variables are suitable for deployment-specific scalar bindings and credential
references; they are not a hidden registry for large workflow definitions.

## Events, audit, and observability

Tangent emits typed operational events such as:

- definition registered, resolved, materialized, rejected, or quarantined
- surface created, activated, suspended, closed, or expired
- interaction submitted, validated, staged, presented, superseded, or expired
- draft saved or deleted
- resolution recorded
- resolution delivery attempted, delivered, acknowledged, or failed
- participant capability granted, consumed, expired, or revoked
- renderer started, failed, or violated policy

Events are published transactionally through an outbox when durability matters.
Tether can carry events when composed, but Tangent's correctness does not
depend on Tether being present.

OpenTelemetry traces, metrics, and structured logs correlate caller transport,
definition resolution, presentation, participant submission, persistence, and
result delivery. Useful measures include:

- active and suspended surfaces
- staged, presented, in-progress, and resolved interactions
- time to first presentation and time to resolution
- reconnect and stale-client counts
- schema and renderer failures by definition revision
- resolution delivery lag and retries
- draft conflicts
- capability denial and sandbox violations
- store, outbox, and registry health

Telemetry carries identifiers, versions, counts, durations, and safe status
metadata by default. Payloads, participant text, diffs, source code,
screenshots, file paths, secrets, and full responses are excluded unless an
explicit diagnostic policy permits them.

Application logs are event streams to stdout and stderr. Durable interaction
history and audit records are not log files.

## Portfolio composition

### Nanite

Nanite owns agent definitions, sessions, conversations, process execution, and
the interactive modes embedded in its chat/runtime experience. Tangent owns
only the separate-window Mode 1 surface.

Nanite can:

- submit an interaction correlated to an agent turn
- launch or focus the Tangent surface
- receive resolution events through the native side channel
- resume the correct agent turn with a typed resolution

Tangent stores opaque Nanite references. It does not launch or manage the
agent, write the conversation, or infer when Nanite considers the turn
complete.

### Tether

Tether is optional. Tangent remains fully usable through direct MCP and its
local host.

When composed, Tether may provide MCP routing, caller identity, session
correlation, messaging, event delivery, or remote gateway policy. Tangent owns
its interaction and renderer settings. Tether owns its gateway and messaging
settings. A deployment binding describes how they compose without making
either application's configuration globally authoritative.

Tether delivery proves that a message or event reached its transport contract;
it does not prove the participant resolved the interaction or the caller
accepted the result.

### Torque

Torque owns work, tasks, leases, review requirements, evidence policy, and
acceptance. It may open Tangent interactions for diff review, evidence review,
approval queues, forms, or exception handling.

Tangent returns authenticated, typed participant evidence. Torque checks the
task revision and acceptance policy and performs the work transition. Tangent
does not mark a task complete or equate a renderer's `approve` button with
Torque acceptance.

### Hadron

Hadron owns workflow definitions, runs, step state, human gates, retries,
compensation, and output bindings. A human interaction step submits a Tangent
request and waits on its handle.

Tangent owns presentation and response capture. Hadron decides whether a
resolution advances, branches, retries, suspends, or fails the workflow. This
is the primary reason Tangent's generic room phases must remain presentation
state rather than a second workflow engine.

### Tesseract

Tesseract owns memory and knowledge namespaces, revisions, relationships,
promotion, and retrieval. Tangent does not automatically convert room history,
participant drafts, or responses into portfolio memory.

An authorized caller may explicitly promote a selected resolution or artifact
reference to Tesseract with provenance and sensitivity policy. Tangent can
retain the resulting knowledge handle as an external outcome reference.

### Fragments Engine and capture systems

Fragments Engine or another content application owns captured material,
provenance, triage policy, and disposition. It can use Tangent for inbox
review, classification correction, routing choices, or attachment inspection.

Tangent owns the interaction record; the content system owns the fragment and
applies the decision. Historical Fast-Triage workflows are reusable surfaces,
not a transfer of general capture or triage authority to Tangent.

### Cerberus and Coder

Cerberus may package, install, configure, register, and observe Tangent as a
local service. The OS or target runtime supervises the process. Cerberus does
not own Tangent rooms, definitions, responses, or participant data.

Coder Workspace resources remain Cerberus-managed infrastructure. Tangent may
render artifacts from a workspace through a scoped caller-provided handle. It
does not provision, discover, or become the workspace authority.

### Envelope libraries and plugin SDK

The envelope libraries own shared protocol types. The Hollis Labs plugin SDK
owns the common registration and lifecycle mechanics used across applications.
Tangent implements an interaction-host contract on both rather than building a
parallel plugin system.

### Other callers

Any MCP-speaking agent or application can use Tangent without participating in
the rest of the portfolio. Standalone operation remains a first-class property.
Composition adds identity, transport, workflow, work, knowledge, or
infrastructure bindings; it does not make those dependencies mandatory.

## Twelve-factor and Go operating model

Tangent follows the portfolio's adapted twelve-factor direction:

- One version-controlled codebase produces versioned binaries and packages for
  many deployments.
- Go and frontend dependencies are explicitly declared and isolated. Optional
  renderer helpers are registered capabilities, not ambient assumptions about
  globally installed tools.
- Configuration is external and contains no secret values. Environment
  variables bind deployment-specific values and credential references.
- SQLite or another database, artifact stores, identity providers, credential
  brokers, Tether gateways, and external source systems are attached resources.
- Build creates an immutable Go binary, embedded frontend, and verified
  renderer assets; release binds them to configuration and registrations; run
  executes that release.
- Processes are disposable. Durable surfaces, interactions, drafts,
  resolutions, and delivery obligations live in an attached stateful store.
- Tangent embeds its own HTTP server and binds an explicitly configured port or
  local endpoint.
- Concurrency scales through durable claims and a coordinated store rather than
  larger process-local room maps or competing uncoordinated writers.
- Startup is deterministic, shutdown is graceful, and in-flight interactions
  and resolution deliveries have explicit recovery semantics.
- Development, test, and production exercise the same definition, renderer,
  identity, persistence, and transport boundaries.
- Logs are structured event streams to stdout and stderr; durable audit and
  interaction history use application stores.
- Migrations, backup, restore, definition validation, cache repair, export,
  retention, and reconciliation run as one-off processes from the same release.

Go-specific conventions follow the broader Hollis Labs engineering direction:

- transports depend inward on application contracts
- domain and application packages do not depend on HTTP, MCP, WebSocket, Wails,
  SQLite, or a particular plugin transport
- constructors validate required dependencies and fail fast
- contexts carry cancellation and deadlines, not optional service dependencies
- interfaces are consumer-owned and intentionally narrow
- errors remain typed across adapters
- process-global mutable state is avoided
- generated code records its source and is reproducibly checked
- official Go clients, SDKs, and CLIs are preferred before bespoke protocol
  implementations

## Current strengths to preserve

The implementation already contains strong architectural choices:

- a crisp separate-window product shape instead of absorbing every interactive
  mode
- one Go binary serving an embedded React application
- explicit loopback binding rather than accidental LAN exposure
- official MCP Go SDK support with Streamable HTTP and legacy SSE compatibility
- a shared envelope catalog instead of ad hoc per-tool wire formats
- namespaced Tangent extensions rather than a forked envelope library
- generated TypeScript envelope types with a staleness check
- multi-room isolation and durable SQLite-backed room history
- explicit submit and cancel semantics
- schema validation on requests and responses
- reconnect-aware pending interaction behavior
- graceful shutdown and embedded database migrations
- durable room reuse across multiple interactions
- a broad library of proven interaction patterns
- artifact references instead of inline base64 payloads in several workflows
- local draft recovery for complex interactions
- a deliberately narrow iframe sandbox for agent-authored HTML
- path-scoped file-picker semantics rather than an unconstrained filesystem
  browser
- append-only histories and revision metadata in several workflow projections
- clear documentation of current limits rather than implied distributed-system
  guarantees
- separation of the HTTP server package from the prospective Wails shell

The target should clarify and generalize these strengths, not replace Tangent
with a generic workflow platform, browser automation system, or full desktop
environment.

## Architectural tensions to resolve

These are target-state design questions exposed by the current implementation,
not a delivery backlog.

### Tangent core contains business workflow state

Room phase fields and writing-flow-specific projections make Tangent the
custodian of interview, synthesis, drafting, revision, output, approval,
dashboard, and other semantics. The target core owns interaction state; the
publisher or Hadron owns business workflow state.

### Each workflow expands several core layers

Many workflows currently add a dedicated MCP handler, schema, response
normalizer, room projection, persistence code, React adapter, draft store, and
tool documentation. This proves useful experiences but makes host evolution
proportional to the number of business interaction kinds. A registered package
contract should make extensions data- and plugin-driven where safe.

### MCP request lifetime is the interaction lifetime

The current push path blocks until a browser responds, cancels, disconnects,
or times out. Human interaction can outlive that transport request. Durable
interaction handles, asynchronous resolution, and delivery state need to be
the authority beneath blocking compatibility calls.

### Browser disconnect closes the room

The active WebSocket currently represents the room strongly enough that losing
it can close the room and fail pending work. Connection, participant, surface,
interaction, and cancellation lifecycles should be independent.

### One active connection and one active advance constrain the domain

A replaceable single connection provides clean refresh behavior, and a single
advance avoids ambiguous responses. Encoding those choices into the Room
object prevents observer connections, robust multi-tab synchronization,
background live updates, and explicit resolver leases.

### Pending interactions are process-local

Pending response channels and claims live in memory. On restart, persisted
pending envelopes are converted to timeouts. Durable staged interactions and
resolution delivery obligations should survive restart according to their own
expiry policy.

### A room is described as an agent session

Docs sometimes say each agent session gets a room or window. This is a useful
default correlation but not an identity invariant. Nanite sessions, MCP
sessions, browser tabs, Tangent surfaces, and external workflow runs have
different owners and lifecycles.

### Room IDs act as locators and capabilities

The current room URL and WebSocket query need only a valid UUID plus a
localhost origin. Loopback limits exposure but does not identify the caller or
participant, isolate local processes, or distinguish view from resolve
authority.

### Localhost endpoints have no caller principal

MCP and session tools can list, inspect, close, or interact with rooms without
a caller-scoped authorization model. Sensitive content and multi-application
composition need explicit principals and capabilities even when everything is
local.

### Mutable room blobs combine authority classes

Phase outputs hold canonical rows, layouts, forms, notes, drafts, selections,
scene snapshots, writing content, and summaries. Some are request snapshots,
some participant drafts, some Tangent presentation state, and some external
business data. Their custody, revision, sensitivity, and retention are not one
uniform JSON-map concern.

### Browser local storage has implicit custody

Several rich workflows save drafts and operator context in local storage. This
is useful recovery behavior, but it can retain sensitive material outside the
server's retention, deletion, encryption, audit, and identity boundaries.

### Resolution capture and business acceptance blur

Approval and completion-shaped UI can imply that Tangent owns the business
transition. Tangent should issue a resolution record; the external application
validates freshness and policy and records acceptance.

### Result capture and result delivery are one call

Persisting a response and returning an MCP result are currently adjacent parts
of one blocking path. If the caller disconnects after the participant submits,
the system needs a durable distinction between captured, delivered, and
acknowledged.

### Definition ownership is split across libraries and Tangent

Generic envelope types come from `go-envelopes`, Tangent types register through
its extension API, and the UI uses generated and manually registered React
components. The exact publisher, source revision, renderer compatibility, and
trust evidence need one inspectable binding.

### Bundled workflows can imply product ownership

Absorbing Fast-Triage and shipping approval, progress, dashboard, writing, and
file workflows makes Tangent useful, but the tool names can make it appear to
own content triage, workflow progress, writing state, or approval policy. These
are interaction packages, not Tangent domains.

### Wails and daemon authority are ambiguous

The current server-plus-SPA shape is designed for a mechanical Wails wrap. A
wrapper can accidentally create two lifecycle owners: a desktop process and a
background daemon. Packaging must preserve one runtime authority and one
store, regardless of presentation host.

### Health reports only process liveness

`/healthz` can succeed while the store, migrations, definition registry,
renderer assets, delivery outbox, or a requested interaction capability is not
ready.

### Persistence policy is broader than the authority model

Rooms retain complete request and response payloads by default while the
README describes per-kind and per-instance persistence. Effective retention,
external references, redaction, and deletion should be enforced by the domain
model, not renderer convention.

### SQLite maintenance and backup are not product contracts

Migrations are explicit, but backup, restore, retention, redaction, repair,
compaction, and reconciliation of pending delivery are not yet one defined
operational contract.

### Untrusted content policy is specialized

Design iteration has a careful iframe sandbox. Other extension renderers,
Markdown, SVG, attachments, exports, links, file previews, and plugin assets
need the same generalized content and effect policy.

### File and action capabilities are encoded in workflow payloads

Allowed roots, attachment refs, export refs, and action outputs are good
building blocks. They require host-enforced capability semantics so a plugin or
caller cannot turn a path string or action ID into ambient filesystem,
clipboard, network, or process authority.

### Observability lacks end-to-end interaction correlation

Structured logs exist, but caller invocation, definition resolution, room
presentation, participant action, persistence, and response delivery are not
one trace and durable event model. Payload-safe instrumentation is especially
important because Tangent handles sensitive material.

### Documentation mixes current and intended topology

The README describes a Wails stack and Nanite-native channel while the current
architecture correctly states that Tangent is an HTTP server plus browser and
the native side channel is future-facing. Direction, current truth, and release
status should remain visibly distinct.

### Versioned architecture documentation lags the product

The architecture document is labeled v0.6 while the project is at v0.12 and
continues to describe some earlier limits. Architecture should describe stable
contracts; release-specific capability lists belong in release documentation.

## Boundary guidance

When deciding whether a capability belongs in Tangent, use these tests.

It belongs in Tangent when it primarily:

- resolves and validates an interaction definition
- opens and manages an app-sized collaboration surface
- renders a versioned interaction safely
- synchronizes surface projections across clients
- persists policy-authorized drafts and interaction operational state
- authenticates and authorizes interaction participation
- captures and seals a typed participant resolution
- delivers or exposes that resolution to the caller
- isolates renderer and plugin capabilities
- operates Tangent's own store, host process, registry cache, and outbox

It probably belongs elsewhere when it primarily:

- defines a business workflow, phase graph, or compensation policy
- creates, schedules, leases, reviews, or accepts managed work
- owns an agent, session, conversation, or model call
- owns captured content, source documents, repositories, or generated output
- manages messages, federation, or general MCP/LLM gateway policy
- owns memory, knowledge, or automatic context promotion
- provisions infrastructure or Coder workspaces
- stores or rotates provider secrets
- applies the business consequence of a participant response
- provides an inline, popover, spatial, or chat-multiplexed interaction mode

If the answer is mixed, keep semantic authority in the owning application and
compose through a versioned `InteractionRequest`, `SurfaceHandle`,
`ArtifactRef`, `ResolutionRecord`, delivery receipt, typed event, or narrow
adapter.

## Questions for the next architecture session

1. What exact distinction and vocabulary should replace the current overlap
   among room, session, surface, envelope, workflow, and interaction?
2. Is a Tangent surface caller-owned, user-owned, or jointly scoped, and what
   are the explicit sharing and transfer rules?
3. Which generic interaction kinds belong in `go-envelopes`, which ship as
   Tangent packages, and which remain application-owned plugins?
4. What immutable definition manifest binds schemas, renderer assets,
   capabilities, trust evidence, retention defaults, and compatibility?
5. Which renderers must be declarative, which may run trusted code, and what
   isolation boundary supports third-party code?
6. What asynchronous handle and subscription contract underlies blocking MCP
   calls and the Nanite-native side channel?
7. When a caller transport disappears, which policy decides whether an
   interaction remains open, is canceled, or expires?
8. What participant identity model works for standalone localhost use while
   composing cleanly with Nanite, Tether, Torque, and Hadron identities?
9. What scoped launch-capability design keeps room URLs usable without making
   UUID knowledge sufficient authority?
10. Which draft custody modes are supported, and how do sensitive interactions
    disable or control browser-local persistence?
11. What resolution and downstream acknowledgement contract distinguishes
    human input, Tangent capture, caller delivery, and business acceptance?
12. Which interaction kinds permit multiple observers, editors, or resolvers,
    and what conflict or lease policy governs them?
13. What artifact handle contract works across local files, Coder Workspaces,
    repositories, screenshots, documents, and application-owned blobs?
14. Which renderer actions are pure responses, and which require explicit
    Tangent-hosted effects with capability mediation?
15. Does the desktop distribution embed the only Tangent host, connect to an
    OS-managed host, or support both as mutually exclusive deployment modes?
16. What data is authoritative in the Tangent store, what is a cache, and what
    backup, restore, redaction, retention, and deletion guarantees apply to
    each custody class?
17. What compatibility surface preserves the proven bundled workflows while
    moving their business-specific phase state out of Tangent core?
18. Which events are durable integration facts, which are live UI projections,
    and which may flow through Tether when present?
19. What readiness and capability-health model lets callers distinguish a live
    process from an available interaction renderer?
20. What is the canonical portfolio vocabulary for the broader interactive
    collaboration modes so Tangent's Mode 1 boundary remains durable?
