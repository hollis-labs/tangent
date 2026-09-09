# ADR 0007: The Collaboration Surface, the Plugin Host, and Where View State Lives

**Status:** Accepted.

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
| Host-defined capabilities, plugin registration points, schema validation (ADR 0003) | Plugin configuration, context recipes, rules and domain behaviour | Partly shipped: capabilities and schema validation are live (`internal/effect/capability.go`, `internal/definition`); the plugin registration point is **decided in §4 and not yet built** |
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

**New. Decided here; not yet built** (`CW-20260909-0042`).

`libs/plugin-sdk` is the portfolio plugin framework. It is host-neutral,
carries `UIComponentTypeEnvelope` as a first-class component type, and
distinguishes a compiled-in plugin from a subprocess one — `UIComponent.Handler`
is documented as compiled-in-only because *"subprocess plugins cannot carry an
http.Handler across the wire."* Nanite is the working consumer precedent
(`plugins/nanite-plugin-oembed`: an envelope type, a JSON schema, `EnvelopeOut`
emission).

**Interaction kinds may be contributed by plugins.** This is the intended path
for new kinds, and `register_all.go` is not the only door.

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

**Deliberately excluded from the first host, and recorded so absence is not
read as decision:**

- **Plugin signing and signature verification.** Out of scope by Chrispian's
  direction, 2026-09-09. First-party compiled-in plugins only. `trust.assurance`
  is unchanged; nothing here relaxes it.
- **Subprocess plugins.** The SDK supports them; the first Tangent host does
  not spawn them. A subprocess host is a separate decision with its own
  isolation questions, and it is not answered by this ADR.
- **Runtime asset loading.** A plugin does not ship a renderer bundle into the
  browser at runtime. The React renderer is compiled into `ui_dist` with the
  release, which is what `core-trusted`'s empty-`asset_digest` requirement
  already assumes.

**Where a plugin's code lives is not the boundary.** In-tree and out-of-tree
plugins are subject to identical manifest and trust rules. The first one may
live in-tree for convenience without that location becoming a permission.

### 5. Long-lived surfaces and host-custodied view state

**New. Decided here; not yet built** (`CW-20260909-0041`).

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

`draft_custody` in the manifest gains a second honest value. `browser-local`
truthfully describes today's kinds, whose drafts live in `localStorage`;
`host-custodied` describes a kind whose drafts are Tangent's records under
ADR 0002. The field continues to describe what the code does, never to grant.

**This does not make Tangent a state authority for the application.** A board's
filter selection is state *about the surface*, owned by ADR 0006 §1's first
row. The records the board displays remain the owning application's, and the
consequence of any action on them remains the owning application's to apply.

### 6. The app-plugin composition pattern

**Intended.** The first instance is `CW-20260909-0043`; this section records
the shape the pilot is testing, not a shipped guarantee.

An application gets an agent-facing surface in Tangent by composing four things
that already have owners:

1. A **domain-free interaction kind**, contributed by a plugin under §4. The
   kind describes a shape — a board of filtered cards with a detail pane — not
   a domain. Torque supplies content to it; it is not a Torque type. This is
   what keeps ADR 0005's *"Tangent is not a task tracker"* true while a Torque
   board renders.
2. **The agent as the application's client.** The agent already holds the
   application's tools; it supplies the data and applies every write. Tangent
   takes no dependency on the application, holds no credential for it, and
   makes no call to it.
3. **Draft revisions for view state** under §5, read on demand.
4. **One room.** A detail view is a pane inside the one envelope, not a second
   pending envelope. `docs/architecture.md`'s one-pending-envelope-per-room
   limitation is respected by composition rather than contested.

The test this pattern must keep passing: **no write to the owning application
originates in Tangent's process.**

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
- §4 and §5 decide things not yet built. They are labeled, but a decision
  without an implementation is a claim about the future, and the labels are the
  only thing preventing them from reading as description.
- Superseding a two-day-old ADR is a cost paid in reader confusion. It is paid
  deliberately: the alternative was a status section that would be wrong again
  by the next release.

### Risks

- **The plugin host is where a boundary would leak most cheaply.** Every
  registration surface the SDK offers that Tangent implements *because it is
  offered*, rather than because a consumer needs it, is a widening. `§4`'s rule
  is the mitigation; `CW-20260909-0042` carries the specific instance
  (`RegisterCRUDHandler` is deliberately left unimplemented).
- **`host-custodied` drafts are a new class of retained participant content.**
  ADR 0002's custody model governs it, and the ADR 0002 §3 precedence engine is
  still unimplemented (`CW-20260905-0008`). Until it lands, a `host-custodied`
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
2. Does §4's rule survive contact with the first real plugin, or does the
   manifest requirement turn out to need an escape hatch? If it needs one, that
   is a new ADR, not a patch to this one.
3. Is `host-custodied` the right name, given ADR 0002 already uses "custody"
   for a different axis?
4. §6 claims no write originates in Tangent's process. Is that testable in CI,
   or only reviewable?

## References

- [ADR 0001](0001-lifecycle-boundaries.md) — interaction lifecycle, delivery record
- [ADR 0002](0002-retention-and-draft-custody.md) — retention and draft custody
- [ADR 0003](0003-definition-and-package-ownership.md) — the definition manifest, package ownership
- [ADR 0004](0004-caller-participant-and-room-access-authority.md) — caller, participant and room access authority
- [ADR 0005](0005-product-boundary-and-portfolio-composition.md) — the product boundary and its tests
- [ADR 0006](0006-collaboration-surface-and-relay-boundary.md) — superseded by this document
- `docs/renderer-trust-classes.md` — the shipped isolation model
- `~/dev/agent-os/workspaces/drafts/tangent/torque-plugin-mvp.md` — the pilot design
