# ADR 0005: Tangent's Product Boundary and Portfolio Composition

**Status:** Accepted. Superseded in part by
[ADR 0006](0006-collaboration-surface-and-relay-boundary.md) (Accepted,
2026-09-07): §1's "conversation host" item, §2's "chat multiplexing across
agents and conversations" bullet, §3's *Agent session and conversation* row,
and §6's "chat-multiplexed" test. Every other section stands unchanged.

**Date:** 2026-09-04

**Approved:** 2026-09-04 by Chrispian, during the Tangent foundation orchestration session

**Task:** `CW-20260825-0067`

**Implementation reviewed:** `a332e76`

**Source:** [`0001-lifecycle-boundaries.md`](0001-lifecycle-boundaries.md) through
[`0004-caller-participant-and-room-access-authority.md`](0004-caller-participant-and-room-access-authority.md),
and the disposition of the retired `docs/interactive-collaboration-direction.md`

## Context

ADRs 0001 through 0004 decide four mechanisms: which lifecycles are separate
(0001), who holds custody of retained content (0002), who owns a definition and
its runtime (0003), and who may act on a surface (0004). Each one answers *how*
Tangent behaves inside a boundary it assumes.

None of them states the boundary. The question "should Tangent own this at all"
had exactly one written answer in the repository, in
`docs/interactive-collaboration-direction.md` — a 1,516-line directional draft
dated 2026-08-22 that also carried a 24-item defect inventory and a curated
"current strengths" list. `CW-20260825-0067` adjudicated all 27 of its factual
claims against `a332e76`: **12 were now false**, 7 partly true, 5 still true,
and **3 were never true when written**. The status half of the document rotted
50% in thirteen days, and its decay was contaminating the durable half — a
reader who checks two claims, finds them stale, and then discounts the boundary
tests has lost the most reusable thing in `docs/`.

The normative half did not decay at all. The portfolio axiom, the
responsibility-boundaries table, the workflow boundary, the composition rules,
and the boundary-guidance tests are as true at `a332e76` as they were at
`f457ad1`, because they are decisions rather than observations. This ADR
promotes them and the source document is deleted rather than stubbed: a file
that holds state will always rot faster than the decision it surrounds, so the
decision is extracted and the state is not preserved.

This ADR states what Tangent refuses to own. It does not define new mechanism,
supersede any part of ADRs 0001-0004, or authorize implementation work; where
the shipped code does not yet match the boundary, this ADR says so and names
the Torque task that closes the gap.

Every claim below is stamped against `a332e76` or explicitly labelled
**intended** or **assumed**. An unstamped normative sentence in this document is
a defect.

## Decision

### 1. The portfolio axiom and Tangent's product definition

The governing axiom for every Hollis Labs tool:

> Hollis tools own execution and operational state, but not the business
> definitions or business data they operate on.

Applied to Tangent, ownership divides as follows. Each row is a decision, not an
observation; the stamp column names where the shipped code already agrees, or
records that agreement is intended.

| Owner | Owns | Stamp at `a332e76` |
|---|---|---|
| Calling application | The purpose of an interaction and the business object it refers to | `InteractionRecord` stores caller correlation opaquely; ADR 0001 §3 |
| Envelope publisher | The interaction type, schemas, semantic meaning, presentation contract, and interpretation of responses | 18 `manifest.yaml` under `internal/envelope/extensions/packages/`; `definition_manifests` (migration 0007), immutable and trigger-guarded (`2e2c48a`) |
| Participant | The meaning of the input or decision they provide | ADR 0001 §9 |
| Business system | Whether that input constitutes approval, acceptance, publication, or completion | `tangent.interaction_acknowledge` (`internal/mcp/interaction_tools.go:123`), whose own description states that reading a result does not acknowledge it |
| `go-envelopes` | The portable protocol vocabulary and common interaction contracts — not any application's business workflow | ADR 0003 §5; the generic-catalog column in §6 |
| Credential authorities | Secrets and their lifecycle | **Intended.** See §3.1; ADR 0004 §12 defers credential custody |
| **Tangent** | The operational interaction record: definition resolution, validation, presentation, surface and connection state, drafts under its custody, typed resolution capture, delivery attempts, and audit | ADRs 0001-0004 in full |

Persisting an envelope or a response does not transfer ownership of the source
document, diff, task, workflow, fragment, workspace, or agent session to
Tangent. Tangent is authoritative for **what it presented and what response it
captured**, never for the external business consequence of that response.

The shorthand:

> Tangent turns publisher-defined interaction contracts into safe, durable,
> app-sized human-agent collaboration surfaces and typed resolutions.

Tangent's product definition is the eight steps of that sentence:

1. Accept a versioned interaction request from an authorized caller.
2. Resolve and validate the publisher-owned interaction definition.
3. Create or reuse a durable surface without claiming the caller's session.
4. Present the request through an appropriate trusted or sandboxed renderer.
5. Capture participant drafts and a typed terminal response under declared
   custody and retention policy.
6. Produce an immutable resolution record and deliver it to the caller.
7. Expose the same interaction semantics through portable MCP and optional
   portfolio-native transports.
8. Provide local operational visibility into active, suspended, resolved,
   expired, and failed interactions.

Tangent is **not** a workflow engine, task manager, agent runtime, conversation
host, general messaging system, content inbox, knowledge authority, document
editor of record, spreadsheet engine, infrastructure control plane, secret
manager, or plugin marketplace.

Tangent can render a workflow step without owning the workflow. It can render an
approval queue without owning approval policy. It can render a diff without
owning the repository. It can capture a form without owning the submitted
business record.

### 2. Mode 1 is a product boundary, not a UI limitation

Tangent is Mode 1 of the Hollis Labs interactive collaboration model: the
separate-window, app-sized surface. The separate-window constraint is a
**decision about scope**, not an artifact of the current implementation, and a
future desktop shell does not relax it.

Tangent owns:

- app-sized, separately addressable interaction surfaces
- persistent surfaces and reopenable interaction state
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

**Stamp.** The shipped product is a Go HTTP server with an embedded SPA served
at `/r/<roomID>`; no Wails code exists in the tree at `a332e76`. Whether the
desktop distribution embeds the host, connects to an OS-managed one, or supports
both is **undecided** — the cross-process `flock` on `<db>.owner` taken by every
writing mode (`8a1ff55`, `docs/database-operations.md` §1) makes either safe
without choosing between them. That choice is open question Q15 below.

### 3. Responsibility boundaries

For each concern, one authoritative owner and one Tangent responsibility. This
table is the operative form of §1 and is the thing to consult when a feature
request arrives without an obvious home.

| Concern | Authoritative owner | Tangent responsibility |
|---|---|---|
| Interaction purpose and business object | Calling application | Preserve typed references and correlation; do not reinterpret |
| Interaction definition | Envelope publisher | Resolve, validate, materialize, cache, and report exact revision |
| Shared envelope protocol | `go-envelopes` and its publishers | Implement the host side consistently |
| Renderer implementation | Definition publisher, subject to Tangent trust policy | Load, isolate, execute, and observe |
| Surface and connection lifecycle | Tangent | Create, reopen, suspend, close, expire, and audit |
| Participant identity | Identity authority or local user session | Bind a principal and capability; record whether it is asserted or authenticated |
| Participant draft | Participant, with declared custodian | Persist only under the interaction's custody and retention policy |
| Typed resolution | Participant for input; Tangent for the capture fact | Validate, timestamp, seal, persist, and deliver |
| Business acceptance | Calling application | Preserve the downstream receipt or outcome reference when supplied |
| Workflow run and gates | Hadron or another workflow owner | Render a gate and return evidence |
| Task and work acceptance | Torque | Render review surfaces and return evidence |
| Agent session and conversation | Nanite, Tether, Torque, Hadron, or another caller | Correlate by opaque reference only |
| Messaging and gateway session | Tether when composed | Consume transport and identity bindings without taking ownership |
| Fragment / content triage | Fragments Engine or another content authority | Render the interaction; do not own content disposition |
| Knowledge and memory | Tesseract namespace authority | Project a pointer or a selected resolution only when explicitly requested |
| Workspace and repository | Cerberus/Coder and the source-control authority | Render scoped artifacts and return selections or annotations |
| Secrets | External credential authority | Accept references or narrow grants; never become the credential store |
| Local service supervision | OS service manager, optionally materialized by Cerberus | Expose lifecycle, health, readiness, and graceful shutdown |

**Stamps and known gaps at `a332e76`:**

- *Interaction definition* is enforced. `internal/definition` plus migration
  0007 hold immutable manifests; `make check-envelopes` detects drift across 44
  kinds (`2e2c48a`).
- *Renderer implementation* is enforced by class. `internal/definition/trust.go`
  orders five trust classes with a strict capability ceiling and
  `internal/server/csp.go` carries the CSP (`37c39e1`). It has **never been
  observed in a browser** — `docs/manual-tests/renderer-sandbox-e2e.md` is the
  only verification, and closing that is `CW-20260904-0171`.
- *Participant identity* is bound but **unverified**. `internal/participant`
  plus migration 0008 give every caller a principal; `internal/authz` grants
  seven capabilities across six principal kinds. The scope is
  `standalone-local` and no identity authority is composed, so the honest
  statement is "no *verified* principal", not "no principal" (`e122b23`).
- *Participant draft* is **not** enforced. `Service.SaveDraft`
  (`internal/interaction/service.go:549`) has no production caller; nine
  browser-local draft families in `ui/src/lib/` are the only custody actually
  running. This is the largest open gap in the table.
- *Typed resolution* and *business acceptance* are enforced and separated:
  captured, delivered, and acknowledged are three distinct states with their
  own records (`internal/interaction`, migration 0006).
- *Local service supervision* is enforced: `/healthz`, `/readyz`, and
  `/healthz/capability[/{kind}]` (`internal/server/health.go`, `75aa2c8`), with
  liveness deliberately touching no database so a probe cannot cause a restart
  loop.

#### 3.1 The secret boundary — intended, not implemented

Tangent does not own provider credentials, API keys, SSH keys, OAuth refresh
tokens, workspace secrets, or application secrets.

Configuration stores credential *references* and non-secret settings. At runtime
Tangent may consume a narrowly scoped, short-lived grant when a host-mediated
effect genuinely requires one. It must not expose that grant to an envelope
payload, renderer, browser storage, plugin config, audit event, or telemetry
stream.

Tangent may provide plumbing that lets Cerberus, a build pipeline, the OS, or a
credential broker materialize a runtime grant. **Plumbing is not custody.**

**Status: intended.** ADR 0004 §12 defers credential custody, and
`CW-20260825-0077` refused to grant `administer` precisely because that decision
does not exist. At `a332e76` `cmd/tangent` constructs a `PrivilegedActorPolicy`
over `effect.Standalone()`, which denies both questions — the composition point
exists and is tested with the deny answer, and nothing has ever been granted
through it (ADR 0004 amendment C2). This section is the only written statement
of the intended secret boundary in the repository; it is recorded here so that
the deferral is a decision with a shape rather than an absence.

### 4. The workflow boundary

Tangent supports multi-turn interaction without becoming a workflow engine.

It **may** own presentation navigation:

- which pane, item, tab, or wizard step is currently visible
- draft completion within one interaction definition
- renderer-local validation and progressive disclosure
- an ordered history of interaction instances on a surface

It does **not** own:

- a reusable workflow language
- cross-application branching or orchestration
- business phase definitions
- retry, compensation, or scheduling for domain work
- task dependencies or completion policy
- whether a response advances an external run

A publisher may define a compound interaction, including a wizard, when the
steps form one bounded user submission. A durable business process spanning
multiple effects and systems belongs in Hadron or the owning domain application.
Tangent receives one interaction at a time and returns typed evidence.

Room-level `current_phase`, `phases_visited`, and arbitrary phase outputs are
therefore **not part of Tangent's target core domain**. Compatibility
projections may remain; future semantics are publisher-owned interaction state
or an opaque external workflow reference. A writing flow, approval process, or
deployment review does not become Tangent-owned merely because Tangent renders
several of its steps.

**Stamp — this boundary is decided and not yet met.** At `a332e76`
`internal/room/phase_state.go:21-25` still declares `CurrentPhase`,
`PhasesVisited`, and `PhaseOutputs` on the core `PhaseState` struct, and twelve
per-kind `*_state.go` files remain in `internal/room`. `25700d0` proved one
migration path (`tangent.form-collect`, whose 591-line state file moved out and
whose residual core footprint is 58 lines) for **1 of 18** kinds. ADR 0003
amendment A3 — accepted 2026-09-04 — assigns a definition's *runtime* to its
publisher on the same terms as its schema, which makes the remaining migrations
mechanical rather than novel. The compatibility surface that preserves the
bundled workflows while the phase state moves is open question Q17.

### 5. Portfolio composition

How Tangent composes with each portfolio tool. Every rule below is a boundary
statement; none of them describes a shipped integration unless stamped.

**Nanite** owns agent definitions, sessions, conversations, process execution,
and the interactive modes embedded in its chat runtime. Tangent owns only the
separate-window Mode 1 surface. Nanite may submit an interaction correlated to
an agent turn, launch or focus the surface, receive resolution events through a
native side channel, and resume the correct turn with a typed resolution.
Tangent stores opaque Nanite references; it does not launch or manage the agent,
write the conversation, or infer when Nanite considers the turn complete.
**Stamp: intended.** No Nanite-native channel exists at `a332e76`; MCP is the
only caller transport.

**Tether** is optional — Tangent is fully usable through direct MCP on its local
host. When composed, Tether may provide MCP routing, caller identity, session
correlation, messaging, event delivery, or remote gateway policy. Tangent owns
its interaction and renderer settings; Tether owns its gateway and messaging
settings. A deployment binding describes how they compose without making either
application's configuration globally authoritative. Tether delivery proves that
a message reached its transport contract; it does not prove the participant
resolved the interaction or the caller accepted the result. **Stamp:**
`internal/smoke` covers direct `/mcp`, legacy `/sse`, and the Tether gateway as
separate paths (`a332e76`); ADR 0001 §8 separates delivery from acknowledgement.

**Torque** owns work, tasks, leases, review requirements, evidence policy, and
acceptance. It may open Tangent interactions for diff review, evidence review,
approval queues, forms, or exception handling. Tangent returns authenticated,
typed participant evidence; Torque checks the task revision and acceptance
policy and performs the work transition. **Tangent does not mark a task complete
or equate a renderer's `approve` button with Torque acceptance.** **Stamp:**
`tangent.interaction_acknowledge` makes this a contract
(`internal/mcp/interaction_tools.go:123`, migration 0006). The residue is
cosmetic and known: the approve and complete renderers still present an
accept-shaped button, which `CW-20260825-0079` folds into the documentation
reconciliation.

**Hadron** owns workflow definitions, runs, step state, human gates, retries,
compensation, and output bindings. A human interaction step submits a Tangent
request and waits on its handle. Tangent owns presentation and response capture;
Hadron decides whether a resolution advances, branches, retries, suspends, or
fails the workflow. This is the primary reason §4's phase-state boundary is not
negotiable — a second workflow engine inside Tangent would compete with Hadron
for the same authority.

**Tesseract** owns memory and knowledge namespaces, revisions, relationships,
promotion, and retrieval. **Tangent does not automatically convert surface
history, participant drafts, or responses into portfolio memory.** An authorized
caller may explicitly promote a selected resolution or artifact reference with
provenance and sensitivity policy; Tangent may retain the resulting knowledge
handle as an external outcome reference. **Stamp: intended.** No Tesseract
integration exists at `a332e76`, which is the correct default — the absence is
the boundary holding.

**Fragments Engine and capture systems** own captured material, provenance,
triage policy, and disposition. They may use Tangent for inbox review,
classification correction, routing choices, or attachment inspection. Tangent
owns the interaction record; the content system owns the fragment and applies
the decision. The historical Fast-Triage workflows are reusable surfaces, not a
transfer of general capture or triage authority to Tangent
(`docs/migrating-from-fast-triage.md`).

**Cerberus and Coder.** Cerberus may package, install, configure, register, and
observe Tangent as a local service; the OS or target runtime supervises the
process. Cerberus does not own Tangent surfaces, definitions, responses, or
participant data. Coder Workspace resources remain Cerberus-managed
infrastructure; Tangent may render artifacts from a workspace through a scoped
caller-provided handle, and does not provision, discover, or become the
workspace authority. **Stamp: partial.** `internal/effect` mints scoped,
expiring, use-counted handles over `os.Root`-walked local roots (`c6a20b2`); a
handle contract spanning Coder Workspaces, repositories, and application-owned
blobs does not exist, and `Capability.scope`'s allowed-origins half has no
consumer (ADR 0003 amendment B6). That is open question Q13.

**Envelope libraries and the plugin SDK.** The envelope libraries own shared
protocol types; the Hollis Labs plugin SDK owns the common registration and
lifecycle mechanics used across applications. Tangent implements an
interaction-host contract on both rather than building a parallel plugin system.
`go-envelopes` defines the portable wire model and reusable schema catalog; the
plugin SDK defines how a package is registered, configured, granted
capabilities, initialized, observed, and stopped; Tangent hosts and renders
interaction instances at runtime; **Sigil** may generate schemas, bindings,
renderer scaffolds, or registry code at build time and is not Tangent's runtime
renderer or plugin authority. Generated Go and TypeScript artifacts must carry
source definition digests so schema drift is detectable rather than inferred
from a successful build. **Stamp:** `@definition-source` digests and
`make check-envelopes` satisfy the last sentence for 44 kinds (`2e2c48a`,
`6bfdaec`). No plugin SDK is composed at `a332e76`; `internal/interactionpkg` is
Tangent's own contract pending one.

**Other callers.** Any MCP-speaking agent or application can use Tangent without
participating in the rest of the portfolio. **Standalone operation remains a
first-class property.** Composition adds identity, transport, workflow, work,
knowledge, or infrastructure bindings; it never makes those dependencies
mandatory.

### 6. Boundary guidance: the tests that decide

When deciding whether a capability belongs in Tangent, apply these tests. They
are the operative form of §1 through §5 and the intended landing place for a
"should we build this here" question.

It **belongs in Tangent** when it primarily:

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

It **probably belongs elsewhere** when it primarily:

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

**Two rules about how this boundary is documented, learned from the source
document's decay:**

1. **A boundary claim carries a stamp or a label.** Every normative sentence in
   a boundary document names the commit, `file:line`, or test that makes it
   true, or is explicitly marked *intended* / *assumed*. An unlabelled claim is
   indistinguishable from a stale one after the first refactor.
2. **A boundary document holds no status.** Defect inventories, tension lists,
   and curated "current strengths" belong in Torque tasks or in tests, never in
   a document whose value is that it does not change. In the source document's
   own "current strengths to preserve" list, three of the bullets were false on
   the day they were written and nothing detected it for thirteen days, because
   a prose list has no owner, no test, and no expiry.

## Consequences

### Positive

1. The question "should Tangent own this" has a written answer with a stamp,
   independent of any status snapshot, for the first time.
2. The four mechanism ADRs gain the boundary they each assume. ADR 0003 §6's
   ownership table and ADR 0004 §2's capability namespaces now sit under a rule
   that says why those splits exist.
3. Known gaps are named as gaps rather than presented as properties: unverified
   participant identity, browser-held draft custody, phase state in core,
   never-observed renderer sandboxing, and an unimplemented custody precedence
   engine each appear with their stamp and their Torque task.
4. `docs/interactive-collaboration-direction.md` is deleted rather than kept
   with a banner, so there is no second source of truth for readers to
   rediscover and no 64 KB file that a banner fails to stop anyone reading.
5. The two documentation rules in §6 are enforceable in review: a boundary PR
   that adds an unstamped claim can be rejected against this ADR.

### Negative and migration risks

1. This ADR states boundaries the shipped code does not meet — §4 in particular.
   A reader who takes §4 as a description of `a332e76` will be wrong; the stamps
   are what prevent that, and they must be maintained as the migrations land.
2. Five ADRs on one subsystem in two weeks is real ceremony, and a contributor
   is likelier to open `docs/architecture.md` than `docs/adr/0005-*.md`.
   Mitigation: the architecture sketch and the shared-application-service
   invariant were promoted into `architecture.md` rather than kept here, and
   `architecture.md` links to this ADR for the boundary.
3. The portfolio composition rules in §5 describe integrations that mostly do
   not exist. They are stamped **intended** where that is the case, but an
   intended rule is still a rule someone can cite as though it shipped.
4. Deleting the source document destroys its unpromoted 60% irrecoverably from
   the working tree. It remains reachable at `f457ad1..a332e76` in git history,
   and the claim-by-claim adjudication is preserved in Tesseract knowledge
   (`tangent/investigations`) rather than in this repository.

## Alternatives considered

### Promote the direction document whole, with a "superseded in part" banner

Rejected. It would install a stale status snapshot as an outward document and
create a second source of truth beside four accepted ADRs. A 64 KB file with a
banner still gets read, and the 12 now-false tension claims would keep
contaminating the 15% that is durable.

### Discard the document entirely

Rejected. The responsibility-boundaries table, the boundary-guidance tests, and
the portfolio-composition rules have no home anywhere else in the repository —
no ADR states them and `architecture.md` does not. Discarding them would
delete the only written answer to "what does Tangent refuse to own".

### Put this content in `architecture.md` §Boundaries instead of a new ADR

Rejected, narrowly. `architecture.md` is a descriptive one-pager; this content
is a set of decisions with live alternatives, because what Tangent refuses to
own is contestable and someone will contest it. A decision with alternatives is
ADR-shaped, and an ADR is dated, stamped, and immutable in a way a descriptive
page is not. The counter-argument — five ADRs is a lot, and contributors read
`architecture.md` — was accepted in part: the two contributor-facing blocks were
promoted to `architecture.md` rather than kept here.

### Keep the tension inventory as a living checklist in the repository

Rejected on the governing principle for this disposition: **files should not
hold state.** A 24-item inventory in a Markdown file has no owner, no test, and
no expiry; it went 50% stale in thirteen days and nothing detected it. Open work
belongs in Torque, where it has an owner and a status transition; durable
evidence belongs in Tesseract, where it is retrievable without being read as
current.

### Convert the six unanswered architecture questions into Torque tasks

Rejected. A task asserts that the work is defined and someone should do it. Q2,
Q8, Q10, Q13, Q15, Q17, and Q20 have no answer yet, and forcing them into task
shape would invent a scope that no one has decided. They are recorded as
questions in this ADR and in Tesseract knowledge; each becomes a task when it
becomes answerable.

## Review questions

1. Is the portfolio axiom in §1 the right division for Tangent specifically, or
   does the operational/business split break down for a product whose entire
   output is a record of someone else's decision?
2. Does §2's Mode 1 boundary survive a desktop shell, or does a native window
   make the "separate-window" constraint vacuous?
3. Is §3's responsibility table complete, and is any row's owner wrong rather
   than merely unimplemented?
4. Is §3.1's secret boundary the right deferral, given that no effect requiring
   a credential has been built yet?
5. Does §4's refusal to own phase state remain correct for the two bundled kinds
   that are not domain-free — `dashboard` and the writing flow?
6. Are §6's two documentation rules enforceable in review, or do they need a
   mechanical check?

The review disposition was: accept this decision as drafted, with the source
document deleted rather than stubbed, and with the six open questions preserved
as questions rather than converted to tasks.

### Open questions this ADR deliberately does not answer

These have no answer at `a332e76` and are recorded so a future architecture
session does not rediscover them. They are also held in the maintainer's Tesseract
knowledge store.

- **Q2 — Is a surface caller-owned, user-owned, or jointly scoped?** ADR 0004
  gives capabilities per principal and never states surface *ownership*;
  `CW-20260825-0075`'s realm-scoped `SameAuthority` judgement was made to force
  a 403/404 split, not because ownership was decided.
- **Q8 — What participant identity model composes with Nanite, Tether, Torque,
  and Hadron?** Still `standalone-local` and explicitly unverified;
  `participant.Store.Rotate` exists with no caller because no identity authority
  is composed.
- **Q10 — Which draft custody modes are supported, and how do sensitive
  interactions disable browser-local persistence?** ADR 0002 §5 names the gate
  and does not build it.
- **Q13 — What artifact handle contract spans Coder Workspaces, repositories,
  and application-owned blobs?** `internal/effect` handles local roots only.
- **Q15 — Does the desktop distribution embed the host, connect to an
  OS-managed one, or support both?** The `flock` contract makes either safe; it
  does not choose.
- **Q17 — What compatibility surface preserves the bundled workflows while
  moving their phase state out?** `25700d0` proved one path for one domain-free
  kind; `dashboard`, `wizard`, and the writing flow are not domain-free.
- **Q20 — What is the canonical portfolio vocabulary for the other interactive
  collaboration modes?** Portfolio-level rather than Tangent-level; it lives in
  Tesseract and not in this repository.

## References

- [`0001-lifecycle-boundaries.md`](0001-lifecycle-boundaries.md) — §1 canonical
  vocabulary, §2 ownership split by authority, §9 drafts are not terminal
- [`0002-retention-and-draft-custody.md`](0002-retention-and-draft-custody.md)
  — §3 custody precedence, §5 the browser draft gate
- [`0003-definition-and-package-ownership.md`](0003-definition-and-package-ownership.md)
  — §5 and §6 ownership, §7 the placement tests, amendment A3 (runtime home),
  amendment B6 (`Capability.scope`)
- [`0004-caller-participant-and-room-access-authority.md`](0004-caller-participant-and-room-access-authority.md)
  — §5 locators are not capabilities, §6 capability material versus scoped
  locators, §12 credential custody deferred, amendment C2
- [`../architecture.md`](../architecture.md) — the architecture sketch and the
  shared application service invariant promoted from the source document
- [`../developing.md`](../developing.md) — the twelve-factor and Go operating
  model promoted from the source document
- [`../database-operations.md`](../database-operations.md) — §1 the single-writer
  contract and the multi-host escape hatch
- [`../host-mediated-capabilities.md`](../host-mediated-capabilities.md),
  [`../renderer-trust-classes.md`](../renderer-trust-classes.md),
  [`../room-workflow-completion.md`](../room-workflow-completion.md)
- [`../../internal/room/phase_state.go`](../../internal/room/phase_state.go) —
  `CurrentPhase`, `PhasesVisited`, `PhaseOutputs` still in core
- [`../../internal/interaction/service.go`](../../internal/interaction/service.go)
  — `SaveDraft` at line 549, no production caller
- [`../../internal/effect/handle.go`](../../internal/effect/handle.go) — scoped,
  expiring, use-counted artifact handles over local roots
- [`../../internal/definition/trust.go`](../../internal/definition/trust.go),
  [`../../internal/server/csp.go`](../../internal/server/csp.go)
- [`../../internal/server/participant_session_test.go`](../../internal/server/participant_session_test.go)
  — `TestKnowingARoomUUIDGrantsNothing`
- Commits on `foundation/tangent-completion`: `2e2c48a`, `e122b23`, `25700d0`,
  `c6a20b2`, `37c39e1`, `75aa2c8`, `3f6ab6c`, `8a1ff55`, `a332e76`
- Torque project `PRJ-20260825-0002`; tasks `CW-20260825-0067` (this
  disposition), `CW-20260825-0079` (documentation reconciliation),
  `CW-20260904-0171` (browser verification of the sandbox)
- The maintainer's Tesseract knowledge (Tangent investigations) —
  the claim-by-claim adjudication of the retired source document
