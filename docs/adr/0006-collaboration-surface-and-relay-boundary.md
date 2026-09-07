# ADR 0006: Tangent as a Collaboration Surface — the Relay Boundary

**Status:** Accepted. Nothing in this document describes shipped capability;
see §6.

**Date:** 2026-09-07

**Approved:** 2026-09-07 by Chrispian, via tangent-16's proxy review, after the
`CW-20260907-0016` spike reported and §6 was amended with its findings

**Task:** `CW-20260906-0056` (planning key V02 of the Tangent vNext capture)

**Baseline reviewed:** `1bb93d4`

**Source:** `~/dev/agent-os/workspaces/planning/tangent-vnext-20260906/ARCHITECTURE.md`
§1 (identity and ownership), with §2, §4, §5 and §6 for vocabulary, the
conductor scenario, relay bindings and history custody; and
[`0005-product-boundary-and-portfolio-composition.md`](0005-product-boundary-and-portfolio-composition.md),
which this ADR supersedes in part.

**Supersedes, in part:** ADR 0005 §1 (the "conversation host" item in the
list of things Tangent is not), §2 (the "chat multiplexing across agents and
conversations" bullet), §3 (the *Agent session and conversation* row), and §6
(the "chat-multiplexed interaction mode" test). Every other section of ADR 0005
stands unchanged, and §5 of this ADR restates the exclusions that are
deliberately kept.

## Context

ADR 0005 decided what Tangent refuses to own. Two of its exclusions were
written for a product whose whole job was to render one interaction at a time
in its own window: Tangent would not be a **conversation host**, and it would
not do **chat multiplexing across agents and conversations**. Both were
correct for the interaction host that shipped.

Daily use has since produced a need the interaction host cannot meet. One user
works with several agents across several projects at once. Each agent already
reaches Tangent over MCP; the durable `/hitl` inbox already gives an agent a
way to ask for attention and an operator a way to answer. What is missing is
the part between those two facts: an addressed, persistent place where the
user can see which agent said what, reply to that agent and not another, know
whether the reply was delivered, and come back later to find out what still
needs handling. That is a conversation surface, and ADR 0005 excludes it.

The vNext architecture capture (`session-20260906-c2c84958`, authorized for
capture by Chrispian) makes the change explicit and narrow: Tangent becomes
*the collaboration surface for one user working with multiple agents across
multiple projects*, and at the same time stays *not a runtime, harness,
conductor, workflow engine, general messaging infrastructure, project database,
or agent-memory authority*. The capture asks for a successor ADR that changes
only the conversation exclusions, preserves the rest, does not recreate the
retired direction document, and keeps the shipped architecture visibly
separate from the target. This is that ADR.

The ADR number is coordinated with the concurrent desktop-shell work: the
shell's decision record (`CW-20260905-0051`) was planned as 0006 and moves to
0007, because this record is written first and the shell record is deliberately
written after its acceptance run.

ADR 0005's two documentation rules apply here in full. Every normative
sentence below carries a stamp against `1bb93d4`, or is labelled **target**.
This document holds no status inventory; open work is named by Torque task id.

## Decision

### 1. Tangent is a collaboration surface, and still not a runtime

Tangent is the collaboration surface for one user working with multiple
agents across multiple projects. It combines addressed conversation, artifact
inspection, structured input, and attention tracking so the user can return to
a discussion and know what needs handling and where their response went.
**Target.**

It is not a runtime, harness, conductor, workflow engine, general messaging
infrastructure, project database, or agent-memory authority. It does not start,
restart, replace, compact, or supervise an agent process. An in-app helper, if
one is ever composed, is optional and never required to route a message.
**Decided; unchanged from ADR 0005 §1 and §5.**

The ownership split that follows. Each row is a decision; the right-hand
column stays with the consumer or external system exactly as ADR 0005 §1
already places it.

| Tangent owns | Consumers and external systems own |
|---|---|
| Channel identities, view state, subject associations, unread and disposition projections | Project and task identities, team topology, workflow state |
| Locally accepted messages, drafts, routing bindings, receipts, and recovery evidence, under explicit custody | External transcripts, agent identity and definitions, session lifecycle, long-term memory |
| Validating and capturing interaction responses (ADR 0001, 0004) | Whether a response authorizes an external action, and whether that action occurred |
| Host-defined capabilities, plugin registration points, schema validation (ADR 0003) | Plugin configuration, context recipes, rules, and domain behaviour |
| Bounded context projections and retrieval access through granted sources | Source content, application data, external context, runtime compaction |

**Stamp.** Rows three and four are the shipped interaction host
(`internal/interaction`, `internal/definition`, `internal/authz` at `1bb93d4`).
Rows one, two, and five are **target**: no channel, message, routing binding,
receipt, or context projection type exists in the tree.

### 2. What changes in ADR 0005, exactly

Four places, and only these four:

1. **§1, the "is not" list.** *Conversation host* is removed from the list for
   the one case this ADR defines: user-facing exchanges addressed to an agent
   destination and held under Tangent custody. Tangent remains not a host for
   an agent's own conversation with its model, which stays with the runtime
   that owns the agent (Nanite, Claude Code, or another CLI).
2. **§2, "Tangent does not absorb".** The bullet *chat multiplexing across
   agents and conversations* is withdrawn. Inline chat controls, ephemeral
   popovers, spatial canvas orchestration, and application-native screens stay
   excluded. Mode 1 — the separate-window, app-sized surface — is unchanged; a
   channel pane is an app-sized surface.
3. **§3, the *Agent session and conversation* row.** The authoritative owner
   is still the runtime. Tangent's responsibility widens from *correlate by
   opaque reference only* to *hold the user-facing exchange record and its
   delivery evidence; correlate the agent's session by opaque reference*.
4. **§6, the "probably belongs elsewhere" tests.** *Provides an inline,
   popover, spatial, or chat-multiplexed interaction mode* loses the word
   *chat-multiplexed*. *Owns an agent, session, conversation, or model call*
   stays, because the agent's conversation is still the runtime's.

Nothing else in ADR 0005 is touched: the portfolio axiom, the workflow
boundary (§4), the secret boundary (§3.1), the composition rules for Torque,
Hadron, Tesseract, Fragments Engine, Cerberus and the plugin SDK (§5), and the
two documentation rules (§6) all stand.

### 3. Vocabulary: channel, thread, view, runtime binding

These four are distinct identities. Conflating any two of them is the failure
this ADR exists to prevent. **Target** throughout; stamps mark where the
shipped code already draws the same line.

| Object | Meaning | It is not |
|---|---|---|
| **Channel** | A persistent collaboration context, independent of any runtime session. May contain several subjects and several participants. | An agent session, a room, a project, or a task. |
| **Thread / subject** | A correlation for one discussion, artifact, or interaction inside a channel. | A task graph, a workflow phase, or a lifecycle of its own. |
| **View** | One concrete window or pane with its own navigation, focus, draft, and recipient selection. | A source of authority. Selecting a URL grants nothing (ADR 0004 §5, `1bb93d4`). |
| **Runtime binding** | The exact external authority, endpoint or session, adapter capabilities, and binding generation a destination resolves to. | The agent. Rebinding is explicit and advances the generation; no automatic choice of the newest session sharing a name. |
| **Exchange** | One message plus its frozen recipient binding, thread, focus revision, context generation, and correlation ids. | A conversation; an exchange is immutable once accepted. |
| **Interaction** | The existing durable structured request, revision, and response authority (ADR 0001 §1). | Renamed or replaced by any of the above. |
| **Attention entry** | A projection of an existing message, request, or delivery problem. | A second request record. |

Rules that follow from the table:

- A **room** stays a compatibility projection of a surface (ADR 0001 §2,
  `internal/room` at `1bb93d4`). A room is not renamed into an agent session
  and is not forced into a channel lifecycle; a channel *associates with*
  surfaces, interactions, and subjects.
- A **non-terminal contribution** (a message in a channel) is distinct from a
  **terminal resolution** (ADR 0001 §9) and does not require the resolver
  lease. Resolving an interaction keeps its existing authority and revision
  checks (`internal/room/connection.go`, `1bb93d4`).
- Project and team groupings are navigation metadata, not isolation
  boundaries. The `standalone-local` partition remains advisory
  (`docs/architecture.md`, current limitations).
- Unread, needs-input, resolved, response captured, delivery pending, delivery
  acknowledged, and external outcome are separate facts. Reading never
  resolves. Delivery never means task completion. Archiving never hides
  unresolved work from the global attention view. This extends ADR 0001 §8's
  captured / delivered / acknowledged split rather than replacing it.

### 4. Custody of operational history

Tangent must retain some state to be a collaboration surface at all. The
boundary on that state:

- **Tangent's relay journal is authoritative for what Tangent accepted and
  delivered, and for nothing else.** It is not authoritative for an external
  agent's conversation, its work, or its memory. **Target.**
- **Three histories stay separate:** the human-visible relay history, a
  helper's working context, and the external runtime's own history.
  Compacting a context does not erase history; deleting history is not a
  reset; resetting a helper does not reset a conductor. **Target.**
- **No mandatory external transcript database, and no covert import of
  runtime transcripts.** External history is referenced, or copied only under
  declared custody. This is ADR 0002's custody model extended to relay
  payloads, not a new transcript store beside it. The recommended default for
  ordinary relay messages is the existing bounded recovery custody; pending
  deliveries and unresolved interactions stay recoverable until an explicit
  disposition; terminal approval and resolution payloads keep their existing
  policy. **Target;** the ADR 0002 §3 precedence engine itself is still
  unimplemented (`CW-20260905-0008`).
- **Tesseract stays the memory authority.** ADR 0005 §5's rule that Tangent
  does not automatically convert surface history into portfolio memory applies
  to channel history unchanged.

### 5. The conductor scenario, and the exclusions that make it work

The representative hard case is two or more independent user-facing
conductors, each privately coordinating several workers, with two or three
active subjects per conductor. **Target.**

- Tangent knows the conductor **destination** and the **user-facing
  exchanges**. Worker references may appear as provenance; they do not become
  recipients or channel members automatically, and Tangent has no worker
  topology API.
- The external conductor host enforces its own team communications contract.
  Tangent does not.
- **No global conversation mutex and no head-of-queue approval restriction.**
  An unanswered review in one team must not block a question in another, or
  a second subject in the same team. Arrival order gives deterministic
  presentation, never a mandatory human execution order. Where a runtime must
  serialize, it serializes per exact destination and is shown as waiting at
  that destination.

The exclusions ADR 0005 already holds, restated here because the conductor
scenario is exactly where each would be tempting to break:

| Kept exclusion | Where ADR 0005 states it | Why it survives this ADR |
|---|---|---|
| Not a runtime, harness, or conductor | §1, §5 (Nanite) | Tangent relays to a conductor; it never becomes one. No agent lifecycle or task hierarchy enters Tangent to make the scenario work. |
| Not a workflow engine | §4 | A thread is a correlation, not a phase graph. Phase state stays publisher-owned. |
| Not a general messaging system | §1, §5 (Tether), §6 | Addressed delivery to a bound agent destination for one user is not federation, gateway policy, or a message bus. Tether stays optional. |
| Not a domain-state authority | §1, §3 | Project, task, team, and workflow identities stay with Torque, Hadron, and the owning application; channels carry them as opaque subject references. |
| Not a memory authority | §5 (Tesseract) | Channel history is operational history under custody, not knowledge. |

### 6. Current versus target

This section is the one a reader must not skip. **At `1bb93d4`, none of the
capability this ADR describes exists.** Specifically:

- There is **no channel, thread, view-instance, runtime-binding, exchange, or
  attention-entry type**, table, migration, API route, or MCP tool in the tree.
- There is **no relay journal, outbox, or delivery receipt** for messages. The
  only delivery evidence Tangent records is the interaction delivery record
  of ADR 0001 §8.
- There is **no addressed conversation** in the SPA. Named room workflows admit
  one pending envelope per room and present one active item
  (`internal/roomflow`, `docs/architecture.md`).
- **The durable `/hitl` inbox is the only shipped attention surface.** It is
  one persistent FIFO ledger across all agents (`internal/hitl`,
  `docs/contracts/hitl-inbox-v1.md`). An agent enqueues an item and may await
  it with a bound; the operator resolves or replies from `/hitl`. It does not
  address a message to an agent, and it does not tell the operator which agent
  session is listening.
- **MCP gives no unsolicited mid-turn injection into a CLI agent.** The loop
  is cooperative: an agent receives only what it calls a bounded await for.
  An agent that is not awaiting is, honestly, *awaiting-peer*; Tangent must
  never present it as reading. This is a property of the transport, not a gap
  a future task closes.
- **Measured, 2026-09-07 (`CW-20260907-0016`).** With the shipped tools and
  five manually launched Claude Code sessions: an operator reply reached an
  agent inside `tangent.hitl_await` in 40–300 ms; a reply that landed while no
  wait was open was returned by the next await or get, so queued input is not
  lost; two concurrent sessions each received only their own reply, and a read
  from another caller scope was refused. What the operator could not do or
  see: speak to an agent that had not enqueued an item, tell whether any agent
  was awaiting right now, or tell whether a reply had been read. Standing by
  cost one model turn per 50 s (the await bound) with a 3–6 s blind spot
  between turns. A session on the default mux proxy could not call any HITL
  tool at all, because the proxy adds gateway trace metadata the strict schema
  rejects (`CW-20260907-0022`). Those are the measured distances between this
  section's current and its target; the full note is
  `~/dev/agent-os/workspaces/drafts/tangent/cooperative-loop-spike.md`.

The plan that moves from current to target is `CW-20260907-0014` (the personal
MVP) and, beyond it, `CW-20260906-0052` (Tangent vNext). The first step, a
zero-schema spike against the shipped HITL tools (`CW-20260907-0016`), has
reported and is folded into the bullet above. Accepting gateway trace metadata
at the MCP boundary (`CW-20260907-0022`) comes next, because without it a
default-configuration session cannot call any tool here; channel bindings, the
journal and outbox, the relay APIs, and the manual Claude proof follow
(`CW-20260906-0064`, `0065`, `0066`, `0071`). No document may describe
any of those as present until its task is done and the shipped build is what
`internal/smoke` derives it to be.

## Consequences

### Positive

1. The narrow change ADR 0005 could not anticipate is made explicitly, with the
   four affected sentences named, rather than by letting 0005 quietly go stale.
2. The vocabulary in §3 gives the vNext tasks one set of nouns to build to, and
   gives review a way to reject a design that collapses a channel into a
   session or a room into a channel.
3. The exclusions in §5 are restated at the point of temptation, so a
   conductor-scenario design that needs a mutex, a worker API, or a scheduler
   can be refused against a written rule.
4. §6 keeps the shipped surface and the target visibly apart, in the ADR
   itself, so the documentation gate (`internal/smoke/docs_test.go`) has
   nothing to catch here and a reader cannot mistake this record for a status.

### Negative and risks

1. Two boundary ADRs now have to be read together. Mitigation: 0005 carries a
   status note pointing at exactly the sections superseded, and this ADR lists
   them in §2.
2. §1's ownership table and §3's vocabulary describe types that do not exist.
   A reader who skips §6 will be wrong about the build. The **target** labels
   and the §6 heading are the defence; they must be maintained as tasks land.
3. "Not general messaging" is a line a relay can drift across one adapter at a
   time. The Tether rule in ADR 0005 §5 — optional, never authoritative for
   Tangent's configuration — is the test to apply to each provider.
4. Acceptance of this ADR authorizes no implementation by itself. The vNext
   capture is explicit that implementation remains manual until Chrispian
   chooses to start it; the personal-MVP plan (`CW-20260907-0014`) is where
   that choice is recorded, task by task.

## Alternatives considered

### Amend ADR 0005 in place

Rejected. ADR 0005's value is that it is dated, stamped, and does not change.
Editing its §2 list would erase the fact that the exclusion once held and why;
a successor with a superseded-in-part note preserves both.

### Supersede ADR 0005 entirely

Rejected. The vNext capture is explicit that the runtime, domain-state, and
general-messaging exclusions must survive. Retiring 0005 whole would reopen
every boundary to relitigation for the sake of changing one.

### Write the target architecture into `docs/architecture.md`

Rejected. `architecture.md` describes the shipped system and carries the
canonical current-limitations list; putting a target there is the exact
mixing of decision and observation that retired
`docs/interactive-collaboration-direction.md` (ADR 0005, context). The target
lives in the planning capture and, for its boundary decision only, here.

### Recreate the direction document with the new scope

Rejected, per ADR 0005 and the vNext capture. That file is deleted and stays
deleted. Open direction goes to Torque tasks; decisions go to ADRs.

## Review questions

1. Is "user-facing exchanges addressed to an agent destination under Tangent
   custody" the right width for the conversation-host exception in §2, or does
   it need a narrower or wider test?
2. Does the exchange model in §3 — immutable, frozen recipient binding,
   correlated by id — hold for a reply that arrives after the binding it was
   sent under has been superseded?
3. Is bounded recovery custody the right default for ordinary relay messages
   (§4), given that the ADR 0002 precedence engine is not yet built?
4. Is §5's refusal of any worker topology API compatible with showing worker
   provenance at all, or does provenance need its own minimal contract?
5. *Resolved 2026-09-07:* the `CW-20260907-0016` spike reported first and §6
   was amended with its findings before this ADR was accepted.

## References

- [`0005-product-boundary-and-portfolio-composition.md`](0005-product-boundary-and-portfolio-composition.md)
  — §1, §2, §3, §6 superseded in part; §3.1, §4, §5 kept in full
- [`0001-lifecycle-boundaries.md`](0001-lifecycle-boundaries.md) — §1 vocabulary,
  §8 delivery versus acknowledgement, §9 drafts are not terminal
- [`0002-retention-and-draft-custody.md`](0002-retention-and-draft-custody.md)
  — the custody model §4 extends
- [`0004-caller-participant-and-room-access-authority.md`](0004-caller-participant-and-room-access-authority.md)
  — §5 locators are not capabilities
- [`../architecture.md`](../architecture.md) — the shipped shape and the
  canonical current-limitations list
- [`../contracts/hitl-inbox-v1.md`](../contracts/hitl-inbox-v1.md) — the one
  shipped attention surface
- `~/dev/agent-os/workspaces/planning/tangent-vnext-20260906/ARCHITECTURE.md`
  §1, §2, §4, §5, §6 — the source capture; `APP-AGENT.md` and `PLAN.md` beside it
- `~/dev/agent-os/workspaces/drafts/tangent/personal-mvp-plan.md` — the
  thinner slice that starts the move from §6's current to its target
- Torque: `CW-20260906-0056` (this record), `CW-20260906-0052` (vNext),
  `CW-20260907-0014` (personal MVP), `CW-20260907-0016` (cooperative-loop
  spike), `CW-20260906-0064`, `0065`, `0066`, `0071` (relay core),
  `CW-20260905-0051` (desktop-shell ADR, now 0007)
