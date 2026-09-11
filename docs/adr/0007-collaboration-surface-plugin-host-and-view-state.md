# ADR 0007: The Collaboration Surface, the Plugin Host, and Where View State Lives

**Status:** Accepted. **§4 narrowed 2026-09-11** and its plugin model extracted
to [ADR 0008](0008-the-plugin-model.md) (`CW-20260911-0040`): the model, the
exclusions and the instance stamps move there, and ADR 0008 reverses two of §4's
exclusions. §4's boundary rule, its reserved-to-host list and its
unimplemented-surfaces posture stand unchanged and stay here. Every other section
stands unchanged. This is an extraction, not a supersession — this document
remains the collaboration-surface record.

**Date:** 2026-09-09

**Approved:** 2026-09-09 by Chrispian, in session `session-20260909-83f81e85`,
who directed that ADR 0006 be superseded rather than amended.

**Task:** `CW-20260909-0040`, under `CW-20260909-0039`.

**Baseline reviewed:** `8702119`.

**Supersedes:** [ADR 0006](0006-collaboration-surface-and-relay-boundary.md)
in full. ADR 0006's decisions are re-adopted here without change of meaning;
what changes is that they are now stamped against a tree that implements them,
and that §6 of that document — a status inventory that was false within two
days — is deleted rather than refreshed.

**Supersedes, in part:** ADR 0005 §1, §2, §3 and §6, in exactly the four ways
ADR 0006 §2 enumerated. That enumeration is restated in §2 below so this
document stands alone. Every other section of ADR 0005 stands.

**Source:** `~/dev/agent-os/workspaces/drafts/tangent/torque-plugin-mvp.md`,
and `~/dev/agent-os/workspaces/planning/tangent-vnext-20260906/ARCHITECTURE.md`
§1 by way of ADR 0006.

## Context

ADR 0006 was accepted on 2026-09-07 with an unusual banner: *"Nothing in this
document describes shipped capability."* It then spent its §6 enumerating, in
detail, what did not exist.

Within two days, most of that section was false. `internal/channel`,
`internal/relay`, `internal/channelpane`, four migrations, five `/api/channels`
routes, seven relay MCP tools and a working SPA channel pane all landed. A
reader arriving at ADR 0006 today is told that a channel type does not exist
while looking at `internal/channel/records.go`.

This is the second time in this repository that a boundary document has decayed
through its status section, and ADR 0005 §6 already wrote the rule that
forbids it:

> **A boundary document holds no status.** Defect inventories, tension lists,
> and curated "current strengths" belong in Torque tasks or in tests, never in
> a document whose value is that it does not change.

ADR 0006 broke that rule in its own successor document. The correction is not
to refresh §6 — a refreshed status section decays on the same schedule — but
to supersede the document with one that carries per-claim stamps and no
inventory.

Two decisions also became necessary that ADR 0006 could not have made, because
the work that raised them had not started:

1. **How new interaction kinds arrive.** ADR 0003 made the definition manifest
   the inspectable binding, and `register_all.go` the mechanical ownership
   table. Neither says whether a kind may be contributed by a plugin, or what
   the portfolio plugin framework is allowed to register.
2. **Where a participant's non-terminal view state lives.** A long-lived
   surface — a board the user filters and browses while an agent works
   alongside — has state that is neither a sealed resolution nor a transient
   render. ADR 0001 covers the resolution. Nothing covered this.

## Decision

### 1. Tangent is a collaboration surface, and still not a runtime

Re-adopted from ADR 0006 §1 without change. Tangent is the collaboration
surface for one user working with multiple agents across multiple projects: it
combines addressed conversation, artifact inspection, structured input and
attention tracking so the user can return to a discussion and know what needs
handling and where their response went.

It is not a runtime, harness, conductor, workflow engine, general messaging
infrastructure, project database, or agent-memory authority. It does not start,
restart, replace, compact, or supervise an agent process. An in-app helper, if
one is ever composed, is optional and never required to route a message.

The ownership split, with each row stamped:

| Tangent owns | Consumers and external systems own | Stamp |
|---|---|---|
| Channel identities, view state, subject associations, unread and disposition projections | Project and task identities, team topology, workflow state | Shipped: `internal/channel/records.go` (`Channel`, `Subject`, `Participant`, `Membership`, `RuntimeBinding`, `ViewFocus`), migration `0013_channels_and_participant_bindings` |
| Locally accepted messages, drafts, routing bindings, receipts and recovery evidence, under explicit custody | External transcripts, agent identity and definitions, session lifecycle, long-term memory | Shipped: `internal/relay/records.go` (`Exchange`, `OutboxItem`, `DeliveryReceipt`, `ReadReceipt`, `Presence`), migrations `0014_relay_journal_and_outbox`, `0015_participant_presence` |
| Validating and capturing interaction responses (ADR 0001, 0004) | Whether a response authorizes an external action, and whether that action occurred | Shipped: `internal/interaction`, `internal/definition`, `internal/authz` |
| Host-defined capabilities, plugin registration points, schema validation (ADR 0003) | Plugin configuration, context recipes, rules and domain behaviour | Shipped: capabilities and schema validation (`internal/effect/capability.go`, `internal/definition`); the §4 plugin registration point (`internal/pluginhost/`, `internal/plugins/`) |
| Bounded context projections and retrieval access through granted sources | Source content, application data, external context, runtime compaction | **Intended.** No context-projection type exists at `8702119` |

### 2. What this changes in ADR 0005, exactly

Unchanged from ADR 0006 §2, restated so this document stands alone. Four
places, and only these four:

1. **§1, the "is not" list.** *Conversation host* is removed for the one case
   defined here: user-facing exchanges addressed to an agent destination and
   held under Tangent custody. Tangent remains not a host for an agent's own
   conversation with its model, which stays with the runtime that owns the
   agent.
2. **§2, "Tangent does not absorb".** The bullet *chat multiplexing across
   agents and conversations* is withdrawn. Inline chat controls, ephemeral
   popovers, spatial canvas orchestration and application-native screens stay
   excluded. Mode 1 — the separate-window, app-sized surface — is unchanged; a
   channel pane is an app-sized surface.
3. **§3, the *Agent session and conversation* row.** The authoritative owner is
   still the runtime. Tangent's responsibility widens from *correlate by opaque
   reference only* to *hold the user-facing exchange record and its delivery
   evidence; correlate the agent's session by opaque reference*.
4. **§6, the "probably belongs elsewhere" tests.** *Provides an inline,
   popover, spatial, or chat-multiplexed interaction mode* loses the word
   *chat-multiplexed*. *Owns an agent, session, conversation, or model call*
   stays.

Nothing else in ADR 0005 is touched: the portfolio axiom, the workflow
boundary (§4), the secret boundary (§3.1), the composition rules (§5), and the
two documentation rules (§6) all stand. This document is bound by both of
those documentation rules.

### 3. Vocabulary, and the two state objects that must not be conflated

ADR 0006 §3 named four identities — channel, thread, view, runtime binding —
and said conflating any two was the failure it existed to prevent. That holds,
and the shipped types draw the same lines (`internal/channel/records.go`,
`internal/relay/records.go`).

This ADR adds a fifth distinction, because the tree now contains two things
that both sound like "what the user is looking at" and are **not the same
object**:

| Object | Scope | Question it answers | Stamp |
|---|---|---|---|
| **`ViewFocus`** | Channel | Which subject and participant is this view pointed at? | Shipped type and store (`internal/channel/records.go:172`, `Store.SetViewFocus`/`GetViewFocus`). **No caller outside the store at `8702119`.** |
| **Draft revision** | Interaction | What has this participant done inside this interaction that is not yet a resolution? | Shipped type, store and service (`internal/interaction/records.go:176`, `Store.SaveDraftRevision`, `Service.SaveDraft`). **No caller at `8702119`.** |

A board's filters, its selection, and whether its detail pane is open are
**interaction state, not channel state**: they are meaningless without the
interaction that defines what a card is. They belong in a draft revision.

Which channel view is focused on which subject is **channel state, not
interaction state**: it survives every interaction opened and closed inside
that channel. It belongs in `ViewFocus`.

A future reader who puts board filters into `ViewFocus`, or channel focus into
a draft payload, has made the mistake this section exists to prevent.

### 4. The plugin host boundary

**New. Decided here.** Shipped: `internal/pluginhost/` (the `plugin_sdk.Host`
implementation) and the `contributedByPlugin` column in
`internal/envelope/extensions/register_all.go` (`CW-20260909-0042`).

**Narrowed 2026-09-11 (`CW-20260911-0040`), and this note is how a reader
arriving here finds the rest.** This section used to carry the plugin *model*
as well as the plugin *boundary* — which mode the host runs in, what a plugin
may not do yet, and which plugin was "the first" one. It was amended twice in
two days, and both times the boundary rule below survived untouched while the
model around it rotted. They have different lifetimes, so they are now in
different documents:

> **[ADR 0008](0008-the-plugin-model.md) carries the plugin model** — Nanite's
> manifest-authoritative registry adopted, runtime bundle loading, first-party
> kinds, compiled-in as the dogfood concession rather than the target, where the
> extracted browser loader lives, and the questions that are open. **It reverses
> two exclusions this section used to state** (runtime asset loading, and
> subprocess plugins as a separate decision) and drops the instance stamps. What
> stays below is what never moved.

`libs/plugin-sdk` is the portfolio plugin framework. It is host-neutral,
carries `UIComponentTypeEnvelope` as a first-class component type, and
distinguishes a compiled-in plugin from a subprocess one — `UIComponent.Handler`
is documented as compiled-in-only because *"subprocess plugins cannot carry an
http.Handler across the wire."*

**Interaction kinds may be contributed by plugins.** This is the intended path
for new kinds, and `register_all.go` is not the only door. ADR 0008 §4 scopes
this to first-party kinds and records that Tangent keeps a small core set of its
own.

The boundary that makes it safe, stated as a single rule:

> **The SDK says what a plugin may offer. The ADR 0003 manifest says what the
> host will let it do. A registration without a manifest is refused.**

Consequences of that rule:

- `RegisterUIComponent(Type: UIComponentTypeEnvelope)` resolves an ADR 0003
  manifest for the declared kind and refuses the registration if there is
  none — the same posture `EnvelopeRouter` already takes toward a kind no
  manifest classifies.
- A plugin **cannot author** its own trust class, granted capabilities,
  effective assurance, or asset digest. ADR 0003 already reserves these to the
  host, and a plugin-supplied `UIComponent` is not a second way to claim them.
- The manifest's `renderer.trust_class` continues to decide isolation and the
  capability ceiling. A plugin-contributed kind is subject to
  `docs/renderer-trust-classes.md` unchanged; `core-trusted` remains
  unreachable for a publisher that is not `tangent` or
  `hollis-labs/go-envelopes`.
- `register_all.go` remains the ownership table for host-package kinds. The
  plugin path is **additive**. The drift tests that make ownership mechanical
  (`TestPackageTreeMatchesRegistrations`) must cover both paths, or the
  guarantee they provide silently narrows to half the registry.

**Host surfaces this host does not implement, recorded so absence is not read
as decision:** `RegisterCRUDHandler`, `GetConfig`, `SetConfig`,
`RegisterConfigSchema` and `GetService`. Each returns an error naming itself
rather than succeeding quietly, because a registration that silently does
nothing is the failure mode worth spending an error on. Implementing one because
the SDK offers it, rather than because a consumer needs it, is the specific way
this boundary rots — see the risk section.

**What this host does not yet do is a question of the model, not the boundary,
and lives in [ADR 0008](0008-the-plugin-model.md).** Subprocess plugins, runtime
bundle loading and plugin signing were listed here as exclusions of "the first
host"; two of the three have since been reversed, and keeping a list of
not-yets inside a boundary statement is what dated this section twice. ADR 0008
§5 and its open questions carry them.

**Where a plugin's code lives is not the boundary.** In-tree and out-of-tree
plugins are subject to identical manifest and trust rules. A plugin may live
in-tree for convenience without that location becoming a permission.

### 5. Long-lived surfaces and `tangent-custodied` view state

**New. Decided here.** Shipped: `interaction.Service.SaveDraft` reached through
`roomDisposition.Draft` and `Room.HandleDraftFrom`, the `draft` WS frame, and
`ws-client.saveDraft` (`CW-20260909-0041`). The round trip is held by
`internal/mcp/room_workflow_draft_test.go` (`CW-20260909-0046`).

A surface may stay open indefinitely while the participant works in it. This
is already possible — `session_advance` with `completion: async` returns a
durable pending receipt and does not cancel the interaction — but until now
nothing could be recorded from such a surface short of resolving it.

**A participant's non-terminal state in a long-lived interaction is recorded as
a draft revision under host custody, and is readable by the caller.**

- The mechanism is the existing `Service.SaveDraft`: revisioned, conflict-
  detecting, custody-aware, gated on the `authz.Draft` capability.
- A stale revision is **refused**, not merged and not silently overwritten.
  The participant's client resynchronizes and retries. Losing a participant's
  view state to a lost race is a defect, not a tradeoff.
- The caller reads it through the existing `tangent.surface_get`, which already
  projects `Drafts []DraftRevision`.
- Reading view state is a **pull**. Tangent does not push a participant's
  in-progress state to a caller, and a caller must not present a draft as a
  decision. A draft is what the user is looking at; only a resolution is what
  the user decided.

`draft_custody` gains nothing. The manifest format has carried five values
since `2e2c48a` — `disabled`, `ephemeral`, `browser-local`, `tangent-custodied`,
`external` (`internal/definition/manifest.go:145`) — and `tangent-custodied` has
been waiting for a kind that meant it. `browser-local` truthfully describes
every kind shipped before now, whose drafts live in `localStorage`;
`tangent-custodied` describes a kind whose drafts are Tangent's records under
ADR 0002. The field continues to describe what the code does, never to grant.

An earlier revision of this section called the value `host-custodied` and said
the format gained it. Both were wrong, and the error is recorded rather than
quietly corrected because this document's whole claim is that its statements
are checkable against the tree: an unstamped value name is exactly the defect
§Context faults ADR 0006 for.

**This does not make Tangent a state authority for the application.** A board's
filter selection is state *about the surface*, owned by ADR 0006 §1's first
row. The records the board displays remain the owning application's, and the
consequence of any action on them remains the owning application's to apply.

### 6. The app-plugin composition pattern

**Decided here.** The first instance is `tangent.app-board`
(`CW-20260909-0043`): its manifest is at
`internal/envelope/extensions/packages/tangent.appboard/app-board/`, its
renderer at `ui/src/components/envelopes/AppBoard.tsx`, and its handler at
`internal/mcp/app_board_handler.go`. This section records the shape the pilot
tests. One instance is not yet a guarantee that the pattern generalizes; what it
does establish is that the pattern is expressible without a host surface being
added for it.

An application gets an agent-facing surface in Tangent by composing four things
that already have owners:

1. A **domain-free interaction kind**. The kind describes a shape — a board of
   filtered cards with a detail pane — not a domain. Torque supplies content to
   it; it is not a Torque type. This is what keeps ADR 0005's *"Tangent is not a
   task tracker"* true while a Torque board renders.

   **Corrected 2026-09-11 (`CW-20260911-0036`, shipped in `adca3ff`).** This item
   said the kind was *contributed by a plugin under §4*, and `tangent.app-board`
   never was one: `publisher: tangent`, `ownership_class: host-package`, a
   renderer compiled into `ui_dist` with the release. It is host plumbing — the
   shape both application plugins fill and neither owns — and it registers
   through `RegisterAll`. A kind a plugin genuinely contributes composes here the
   same way; which door the kind came through was never what this pattern
   depended on, which is why the correction changes the sentence and not the
   pattern.
2. **The agent as the application's client.** The agent already holds the
   application's tools; it supplies the data and applies every write. Tangent
   takes no dependency on the application, holds no credential for it, and
   makes no call to it.
3. **Draft revisions for view state** under §5, read on demand.
4. **One room.** A detail view is a pane inside the one envelope, not a second
   pending envelope. `docs/architecture.md`'s one-pending-envelope-per-room
   limitation is respected by composition rather than contested.

The test this pattern must keep passing: **no write to the owning application
originates in Tangent core, and a plugin that writes to one is a knowingly
recorded exception with a tracked end date.**

**Amended 2026-09-10 (`CW-20260910-0031`), because the first real app plugin
crossed the line the original sentence drew.** As written, the test was "no
write to the owning application originates in Tangent's *process*". The Torque
board plugin makes Torque writes from inside that process, because this host
compiles its plugins in. Chrispian accepted that tradeoff knowingly for the
prototype — using it sooner outweighs the purity — and `CW-20260910-0034`
(plugin-sdk subprocess mode) is the real fix, with this workload as its first
motivating case (Tesseract `agents_drive_tangent_apps_are_called`).

The amendment is deliberately narrow, and the parts it does not relax are the
parts that were doing the work:

- **Tangent core still holds no application dependency.** `internal/plugins/torque/torque.go`
  is the only file in the repository that knows Torque exists. Nothing in
  `internal/mcp`, `internal/room`, `internal/interaction` or the renderer
  learns a Torque concept, which is what keeps ADR 0005's *"Tangent is not a
  task tracker"* true while a Torque board renders.
- **`RegisterCRUDHandler` stays unimplemented.** A plugin holding its own
  application's client is userland taking a dependency it chose. A *host*
  surface that writes to applications on a plugin's behalf is Tangent growing
  one, and that is still refused.
- **The plugin is a caller, not an insider.** It drives Tangent through
  `pluginhost.ToolCaller` — the same MCP tool surface an agent calls, resolving
  to the same host-assigned caller identity every local MCP caller gets. It
  holds no handle to the database, the room manager or the interaction service,
  and `GetService` remains unimplemented so it cannot acquire one.

Recording the exception here rather than leaving the sentence quietly false is
the point. A document whose claims are checkable against the tree is this ADR's
whole argument for existing (see §Context on ADR 0006); an absolute that the
tree contradicts would be the same defect one section later.

**Amended again 2026-09-10 (`CW-20260910-0054`), because the second app plugin
takes the same exception, and amending this section once per plugin would turn a
boundary into a changelog.** The amendment above names a plugin. The Tesseract
plugin makes Tesseract writes from inside this process for exactly the reason
the Torque board does — this host compiles its plugins in — and nothing about
that is specific to either application.

The exception is therefore restated as a property of **the host**, not of a
plugin:

> While this host compiles its plugins in, a compiled-in plugin's writes to the
> application it adapts originate in Tangent's process. That is a known cost of
> the compiled-in mode, tracked by `CW-20260910-0034`, and it closes for every
> plugin at once when subprocess mode lands.

**The mode named above is corrected 2026-09-11 (`CW-20260911-0040`), and only
the mode.** Both amendments were written as though compiled-in were the host's
standing mode and subprocess an improvement that might arrive. That is
backwards: compiled-in was the **dogfood concession**, authorized so the Torque
integration could be used sooner, and subprocess plus runtime UI loading is the
target ([ADR 0008](0008-the-plugin-model.md) §1 and §5).

That correction is about which mode this host runs and why. It leaves the test
above, its exception and its terms exactly as they stand.

Two conditions keep the exception readable rather than merely recorded:

- **Every plugin that takes it says so in its package doc**, in the terms
  `internal/plugins/torque/plugin.go` already uses. Who is inside the
  exception is then a `grep`, not a memory.
- **A plugin that writes to an application holds that application's client in
  its own package and nowhere else.** `internal/plugins/torque/torque.go`
  is the only file in the repository that knows Torque exists; a Tesseract
  plugin's client must be the only file that knows Tesseract does. Widening the
  exception widens what a *plugin* may do and not what Tangent core learns,
  which is the half that was doing the work.

What this does not relax, unchanged from the first amendment:
`RegisterCRUDHandler` stays unimplemented, `GetService` stays unimplemented, and
a plugin remains a caller through `pluginhost.ToolCaller` rather than an insider
holding a handle to the database, the room manager or the interaction service.

One thing this amendment adds rather than restates, because the second plugin
raises it and the first did not:

- **A plugin writes with whatever authority its own client carries, and Tangent
  grants it none.** Tangent holds no credential for an application and issues no
  identity to one. A plugin writing to a store that trusts an asserted actor is
  relying on that store's policy, not on a guarantee from this host, and must not
  be described as though the host vouched for the write. `CW-20260910-0045` is
  where that is being made enforceable for Tesseract specifically; nothing here
  anticipates its outcome.

## Consequences

### Positive

- A reader who opens the collaboration ADR is no longer told that shipped types
  do not exist.
- Every normative claim here names a file, a type, a migration or an explicit
  *intended* / *assumed* label, so a future reader can check it rather than
  trust it.
- The plugin path for new kinds is decided, so contributing a kind stops being
  a question answered per-kind by whoever asks.
- `SaveDraft` and `ViewFocus` — two complete, unwired subsystems — get owners
  and a stated difference before either acquires a caller that guesses wrong.
- The app-plugin pattern is written down before its first instance, so the
  pilot can falsify it.

### Negative and costs

- Three ADRs now bear on the same boundary: 0005 (the axiom and the tests),
  0007 (this), and 0003 (the manifest). A reader needs 0005 §6 and 0003 §2 to
  apply §4 here. Consolidating them is not attempted; three accurate documents
  beat one document that has to be re-litigated to merge.
- §4 and §5 were decisions before they were implementations. They are now
  stamped against the files that implement them (`CW-20260909-0042`,
  `0041`, `0043`, `0046`), which is what this document asks of every other
  normative claim. The cost was real while it lasted: for the interval between
  acceptance and implementation, the labels were the only thing preventing two
  sections from reading as description.
- Superseding a two-day-old ADR is a cost paid in reader confusion. It is paid
  deliberately: the alternative was a status section that would be wrong again
  by the next release.

### Risks

- **The plugin host is where a boundary would leak most cheaply.** Every
  registration surface the SDK offers that Tangent implements *because it is
  offered*, rather than because a consumer needs it, is a widening. `§4`'s rule
  is the mitigation; `CW-20260909-0042` carries the specific instance
  (`RegisterCRUDHandler` is deliberately left unimplemented).
- **`tangent-custodied` drafts are a new class of retained participant content.**
  ADR 0002's custody model governs it, and the ADR 0002 §3 precedence engine is
  still unimplemented (`CW-20260905-0008`). Until it lands, a `tangent-custodied`
  kind's retention is the host default, and no kind should author a looser
  `retention_class` on the assumption that precedence will tighten it later.

## Alternatives considered

### Amend ADR 0006 §6 in place

Rejected. The section would be accurate for as long as it took the next
capability to land — two days, last time. ADR 0005 §6 already forbids the
section's existence, and amending it would preserve the thing that failed.

### Mark ADR 0006 "Accepted, partially implemented" and move on

Rejected. It defers the two decisions in §4 and §5 that the current work
actually needs, and leaves the false claims in place while adding a banner that
does not say which claims are false.

### Let `register_all.go` remain the only door for new kinds

Rejected by Chrispian, 2026-09-09: plugins are how new envelopes are meant to
arrive, and the portfolio already has the framework and a working consumer in
Nanite. Keeping the single door would make every new application surface a
change to Tangent's own registration table, which is precisely the coupling the
plugin framework exists to remove.

**Scoped 2026-09-11:** *"how new envelopes arrive"* means **first-party**
plugins, and Tangent keeps a small core set of kinds in `register_all.go` that
rarely changes ([ADR 0008](0008-the-plugin-model.md) §4). The rejection stands;
read unscoped it would suggest a third-party contribution path that is not in
scope and would reopen the whole trust model.

### Put board filters in `ViewFocus` rather than a draft revision

Rejected. `ViewFocus` is channel-scoped and survives the interaction; board
filters are meaningless without the interaction that defines a card. Reusing it
would conflate two identities, which ADR 0006 §3 named as the failure mode this
vocabulary exists to prevent.

### Give Tangent a Torque client

Rejected. It would put application knowledge, a credential and a network
dependency in Tangent's process to save the agent a round trip the user does
not notice, and it would make the *"no write originates in Tangent"* test in §6
unverifiable.

## Review questions

1. Does any normative sentence here lack a stamp or an *intended* / *assumed*
   label? If so it is a defect, not a style preference.
2. **Answered 2026-09-11, and not the way this question expected.** §4's rule
   survived contact with three plugins and needed no escape hatch — the manifest
   requirement has not been relaxed once, and `RegisterUIComponent` still refuses
   a kind it cannot resolve a manifest for. What needed a new ADR was everything
   *around* the rule: the mode, the exclusions and the instance stamps, which
   moved twice in two days while the rule did not move at all. That is
   [ADR 0008](0008-the-plugin-model.md), and it is an extraction rather than the
   patch this question was trying to forbid.
3. **Resolved 2026-09-09, against this document.** There was never a naming
   choice to make: `tangent-custodied` shipped in the manifest format at
   `2e2c48a`, long before this ADR, and the enum is what validates. This
   document said `host-custodied`, which never existed. Prose corrected; no
   manifest-format change, and `tangent.app-board` authors the real value.

   What remains genuinely open is narrower: ADR 0002 uses "custody" for the
   retention axis, and `draft_custody` uses it for a location axis. Two
   meanings, one word, in adjacent documents. Worth a rename only if a reader
   is actually observed conflating them — the discrepancy above cost one
   session a paragraph, not an outage.
4. §6 claims no write originates in Tangent's process. Is that testable in CI,
   or only reviewable?

## References

- [ADR 0001](0001-lifecycle-boundaries.md) — interaction lifecycle, delivery record
- [ADR 0002](0002-retention-and-draft-custody.md) — retention and draft custody
- [ADR 0003](0003-definition-and-package-ownership.md) — the definition manifest, package ownership
- [ADR 0004](0004-caller-participant-and-room-access-authority.md) — caller, participant and room access authority
- [ADR 0005](0005-product-boundary-and-portfolio-composition.md) — the product boundary and its tests
- [ADR 0006](0006-collaboration-surface-and-relay-boundary.md) — superseded by this document
- [ADR 0008](0008-the-plugin-model.md) — the plugin model, extracted from §4
- `docs/renderer-trust-classes.md` — the shipped isolation model
- `~/dev/agent-os/workspaces/drafts/tangent/torque-plugin-mvp.md` — the pilot design
