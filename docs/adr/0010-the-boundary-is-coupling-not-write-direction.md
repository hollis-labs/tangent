# ADR 0010: The Boundary Is Coupling, Not Write Direction

**Status:** Accepted.

**Approved:** 2026-09-17 by Chrispian, in chat, after reviewing the draft and
its two open review questions: *"Approved and let's commit/push."*

**Drafted `Proposed`, promoted on approval, the standing convention for an
agent-drafted ADR since ADR 0008.** Both open review questions in §*"Open, for
Chrispian's review"* are resolved by this approval as answered in the
affirmative — no correction was raised against either — and the section is
left in place as the record of what was asked.

**Date:** 2026-09-17

**Task:** `CW-20260911-0057`

**Baseline reviewed:** `67b4378`.

**Supersedes, in part:** [ADR 0007](0007-collaboration-surface-plugin-host-and-view-state.md)
§6, in exactly the two ways *"What this supersedes"* below enumerates. §6 is
not edited — it remains the accurate log of what was decided on 2026-09-09,
2026-09-10 and 2026-09-11, including the amendments already recorded there.
This document carries the reversal and the correction; the log stays the log.

## Context

ADR 0007 §6 states the test an application plugin must keep passing: *"no
write to the owning application originates in Tangent core, and a plugin that
writes to one is a knowingly recorded exception with a tracked end date."*
Two application plugins (Torque, Tesseract) have since taken that exception,
each amendment restating it more generally, each one leaving the underlying
sentence — writing to an owning application is a violation Tangent tolerates
— unquestioned.

Chrispian ruled 2026-09-11, through a Tangent approval queue, that the
sentence itself is wrong, not merely inconvenient:

> That ADR about not publishing from tangent is a prime example. That ruling
> sounds good in theory but the logic doesn't hold up. It's solving a problem
> that doesn't exist.

His reasoning, in full, because it is the part that outlives the edit:

- **Authority runs the opposite way from what the rule assumed.** *"If I'm in
  tangent approving and amending — that session has more authority than ANY
  other write path because it came from me, programmatically, right into
  Tesseract. Today, every other write path is from an agent."* A human
  pressing approve inside a Tangent surface is the highest-authority write
  available in this system. The rule treated it as the most suspect.
- **The plugin is the mechanism, and forbidding its purpose defeats it.**
  *"The point of the plugins is to make the writes between Tangent and any
  other app deterministic, structured, testable, auditable, and replyable."*
- **Responsibility sits with the plugin and the app it adapts, not with
  Tangent.** *"Tangent is not responsible for those things, the plugin and the
  app consuming them is."*
- **The rule is the seam it claims to guard against.** *"If we make that rule
  we've already violated an earlier design principle and we are enforcing a
  workflow/process and that's the real seam."*

That is the lens, stated once and explicitly not to be reopened as a rule:

> We don't own the consumers['] data, workflows, processes, rules, judgement,
> nothing. We provide a tool/service and we only have process, rules, guards,
> etc. where necessary for safety, stability, and to provide the functionality
> the app is trying to implement.

Restated and extended by Chrispian on 2026-09-17, at the point this ADR was
requested, in terms that name what the lens implies and had not yet been said
plainly:

> Tangent is a service/tool provider. It does not set policy about how users
> use it. That's the real seam, as with all our other apps. Messaging
> (reading/writing) is vital. And sending updates like syncing, etc, are
> useful to the user. This is not a read-only app. It was never meant to be.
> The boundary between apps is about tight coupling, none of our apps can
> require another app to function (except by design — Tangent is meant for
> other apps to consume).

Two things follow from that which §6's test did not distinguish: **what
Tangent is** (a tool/service layer, not a policy-setter over how a consuming
app or its user works), and **what the actual portfolio boundary protects**
(no app may require another to be present in order to function, except by
explicit design — which is exactly what Tangent, as the thing other apps are
meant to consume, already is). Neither of those is a claim about which
direction a byte travels.

A second, independent fact has changed since §6 was last amended. §6 names
`pluginhost.ToolCaller` as *the* mechanism by which a plugin drives Tangent.
`CW-20260911-0070` (subprocess migration, landed `e88a601`) has since made
that true of one mode only. Verified at this baseline:

```
$ go list -deps ./cmd/tangent | grep -c 'plugins/torque\|plugins/tesseract'
0
```

Both first-party plugins now run as independent processes
(`cmd/tangent-plugin-torque`, `cmd/tangent-plugin-tesseract`), installed under
the plugin root and spawned as subprocesses — confirmed running that way at
this baseline. The plugin-sdk subprocess wire is strictly host-initiated (the
subprocess never sends a request), so a subprocess plugin cannot call back
through `ToolCaller` at all. It reaches Tangent the way any other local
process does: as an ordinary MCP client against `/mcp`, granted no authority
beyond what any local MCP caller already has. `ToolCaller` remains correct
for a compiled-in plugin; it is no longer the only path.

## Decision

### 1. Tangent is a service/tool provider, not a policy-setter

This is the seam, stated as what it actually is rather than as a rule about
write direction: **Tangent supplies mechanism — a durable interaction
substrate, a room, a plugin host — and holds no opinion about how a consuming
application or its user chooses to use it.** That is a portfolio-wide pattern,
not a Tangent-specific one; every Hollis Labs app that provides a service to
others holds the same posture toward its consumers.

This is deliberately not written as a new checklist, a new AGENTS.md
boundary, or a new test. Writing the lens itself as a rule would repeat the
exact defect this ADR corrects — a document meant to describe the shape of a
decision curdling into an instrument for grading unrelated ones. Where a
guard is genuinely needed, §3 below says what still justifies one.

### 2. The real inter-app boundary is coupling, not direction

**No Hollis Labs app may require another app to be present and reachable in
order to function — except where that is the explicit design.** Tangent is
the explicit exception on the *depended-upon* side: it exists to be consumed
by other apps, and an application plugin taking Tangent as a dependency is
the pattern working as intended.

What the boundary actually protects, restated against the mechanism that
enforces it: **Tangent's own binary takes no dependency on any application it
serves.** That is checkable, not asserted:

```
$ go list -deps ./cmd/tangent | grep -c 'plugins/torque\|plugins/tesseract'
0
```

`internal/plugins/torque/torque.go` is still the only file in the repository
that knows Torque exists, and the equivalent holds for Tesseract. That
property — Tangent stays domain-free — is what ADR 0005 protects and what
this ADR leaves completely untouched. §6's test conflated two different
questions: *"does Tangent depend on the application"* (still no, and still
enforced) and *"may a write to the application originate from code Tangent
loaded"* (the question this ADR answers: yes, through a plugin, by design).

### 3. §6's no-write test is dropped, and nothing structural replaces it

The sentence — *"no write to the owning application originates in Tangent
core, and a plugin that writes to one is a knowingly recorded exception with
a tracked end date"* — no longer holds. A plugin's write to the application
it adapts is not an exception, tracked or otherwise; it is the plugin doing
what a plugin is for.

**This does not relax anything that was doing real work for a different
reason.** Three surfaces stay exactly as unimplemented as they were, and the
reason each one stays that way is restated here because the old reason (writes
are suspect) is gone and the surviving reason is not the same sentence:

- **`RegisterCRUDHandler` stays unimplemented.** Not because a write to an
  application is forbidden — it isn't — but because a *host* surface that
  writes to applications generically would require Tangent to learn an
  application's schema, which is the domain-free-binary property §2 restates
  and ADR 0005 protects. A plugin holding its own application's client is
  userland taking a dependency it chose; a host surface doing the same work on
  every plugin's behalf is Tangent growing one. Nobody needs this today, and
  implementing a host surface because the SDK offers one — rather than because
  a consumer needs it — is the specific way ADR 0007 says this boundary rots.
- **`GetService` stays unimplemented**, for the same reason stated in ADR
  0007 §6 and unchanged by this ADR: a plugin is a caller, not an insider. It
  holds no handle to the database, the room manager or the interaction
  service, and drives Tangent through the same tool surface any local MCP
  caller uses.
- **Tangent grants a plugin no authority beyond its own client's.** A plugin
  writes to the application it adapts with whatever credential that client
  carries; Tangent holds none of it and vouches for none of it. This is
  unchanged from §6's second amendment and is restated because it is the
  actual safety property in this area, not the write-direction rule that is
  being dropped.

### 4. Messaging and outbound sync are vital, intended capabilities

Tangent is not a read-only surface and was never meant to be one. Bidirectional
messaging (an agent or the operator reading and writing through a room,
channel or durable inbox) and an application plugin sending updates back to
the app it adapts — a Torque board's Sync button is the shipped example — are
both core to what Tangent is for, not tolerated exceptions to a rule that
happened not to catch them yet.

### 5. `ToolCaller` is one mode's mechanism, not the only one

§6's description of `pluginhost.ToolCaller` as *the* way a plugin drives
Tangent is corrected to what is now true of two modes:

- A **compiled-in** plugin drives Tangent through `pluginhost.ToolCaller` — an
  in-process MCP client session against Tangent's own tool surface, resolving
  to the same host-assigned caller identity every local MCP caller gets.
- A **subprocess** plugin cannot use `ToolCaller` — the plugin-sdk wire is
  host-initiated only, so a child process has no channel to call upstream on.
  It reaches Tangent by connecting to Tangent's own `/mcp` as an ordinary
  local MCP client, over the same loopback origin guard and the same
  host-assigned identity every other local caller resolves to. This grants a
  subprocess plugin no more authority than it already had as a local process
  on the machine; it is the intended path made real rather than a workaround.

Both are instances of the same rule: a plugin is a caller against Tangent's
own tool surface, in whichever process it happens to run.

### 6. Torque's (and Tesseract's) recorded "exception" is description, not confession

`AGENTS.md`'s current text — *"Its compiled-in Torque writes are a knowingly
recorded exception to §6, amended there rather than left quietly false"* — and
the equivalent framing in each plugin's own package doc are corrected under
this ADR to describe what the plugin does, plainly, rather than to confess an
exception to a rule that no longer exists. What survives from that language
is worth keeping: a plugin that writes to an application should still say so
in its own package doc, and should still be the only file in the tree that
knows that application exists. Those are real properties. They stop being
framed as violations.

## What this supersedes

Named precisely, so a reader of ADR 0007 §6 knows what still holds.

**§6's test sentence** — *"no write to the owning application originates in
Tangent core, and a plugin that writes to one is a knowingly recorded
exception with a tracked end date"* — is **dropped**, per §3 above.

**§6's description of `pluginhost.ToolCaller`** as the mechanism a plugin
uses is **narrowed to the compiled-in mode**, per §5 above.

**Not superseded, and worth saying because it sits right next to what
changed:**

- §6's composition pattern itself — a domain-free interaction kind, the agent
  or plugin as the application's client, draft revisions for view state, one
  room — is untouched.
- §6's statement that Tangent core holds no application dependency is
  untouched and is what §2 above restates against the current, checkable
  fact.
- §6's statement that a plugin writes with its own client's authority and
  Tangent vouches for none of it is untouched and is restated in §3 above.
- `RegisterCRUDHandler` and `GetService` staying unimplemented is unchanged in
  outcome; §3 above restates why, because the old reason no longer applies and
  a surviving unimplemented surface needs a reason that is still true.

## Consequences

### Positive

- **The record matches what Chrispian has already ruled**, closing a gap
  where AGENTS.md and two plugins' own package docs stated a rule as live
  policy that he had reversed six days earlier.
- **Torque's and Tesseract's write paths stop reading as tolerated
  violations.** They were always the pattern working; now the documentation
  says so.
- **The `ToolCaller` correction is recorded at the point it became true**,
  rather than either before the migration (projection) or silently after it
  (drift).
- **The lens is on the record without being turned into an instrument.** A
  future decision can be checked against it without it becoming a checklist
  a reviewer runs.

### Negative and costs

- **A documentation sweep is needed**, not a code change: `AGENTS.md`'s
  Torque paragraph and the "no write... may originate in Tangent's process"
  line, plus each application plugin's own package-doc framing. None of this
  moves a manifest, a contract digest, or a wire shape — it is prose
  corrected to match a decision already made.
- **A reader who finds only ADR 0007 §6** — by `grep`, by an old link, by not
  knowing this document exists — sees a rule that no longer holds. Mitigated
  the same way ADR 0008 and ADR 0009 mitigate it: this document's header, and
  a pointer added at §6's own location in AGENTS.md's index.

### Risks

- **This is misread as removing guardrails generally.** It removes exactly
  one claim — that a write originating in Tangent-loaded code is inherently
  suspect — and states plainly what still holds: Tangent takes no application
  dependency, holds no application credential, and grants no authority beyond
  what a plugin's own client carries. A future reader extending "writes are
  fine now" into "a host surface may hold an application's credential" would
  be reading past §3, not applying it.
- **"Tangent does not set policy about how users use it" is misread as
  license for a plugin to do anything.** It is not — §3's three unimplemented
  surfaces, and the ADR 0004 capability checks every plugin route and tool
  already passes through, are exactly the "safety, stability, and the
  functionality the app is trying to implement" the lens names as the actual
  ground for a guard.

## Alternatives considered

### Amend ADR 0007 §6 in place, a fourth time

Rejected for the same reason ADR 0008 and ADR 0009 gave for the same choice.
§6 has already been amended three times, each amendment attached to the
section rather than replacing it, and each one left the original test
sentence unquestioned. A fourth amendment preserves the structure that kept
failing. An accepted ADR is a log of a decision that was made; a reversal is
a new record, and §6 stays exactly as accurate a log of 2026-09-09 through
2026-09-11 as it was before this document existed.

### Fold this into ADR 0009

`CW-20260911-0057`'s own filing suggested this, since both are supersessions
of adjacent material landing close together. Not taken: ADR 0009 was
approved and shipped on its own six days before this document was drafted, so
there is no longer a single commit these could share.

### Leave the rule as unenforced guidance rather than superseding it

Rejected. The rule was never mechanically enforced — nothing in CI checked
that a write did not originate in Tangent's process — so demoting it to
"guidance" would change nothing about what runs and would still misstate what
Chrispian has ruled. The problem is that the sentence is wrong, not that it
lacked a gate.

## Open, for Chrispian's review

1. **Does §3's restatement of why `RegisterCRUDHandler` and `GetService` stay
   unimplemented match your intent, or does the reversal change your answer
   for either of them?** This document's position is that it does not — the
   domain-free-binary reason was always independent of the write-direction
   rule — but that is this document's inference, not a ruling already made.
2. **Is the AGENTS.md and package-doc sweep in §6 above scoped correctly?**
   Drafted alongside this ADR (see the accompanying diff) rather than held
   for a second pass, on the reasoning that leaving the reversed sentence live
   in AGENTS.md one day longer than necessary repeats the exact "stated but
   disbelieved" state this ADR exists to close.

## References

- [ADR 0005](0005-product-boundary-and-portfolio-composition.md) — the product
  boundary; the domain-free-binary property §2 and §3 restate
- [ADR 0007](0007-collaboration-surface-plugin-host-and-view-state.md) §6 —
  the app-plugin composition pattern and the test this document drops
- [ADR 0008](0008-the-plugin-model.md) — the plugin model; the supersession
  convention this document follows
- [ADR 0009](0009-renderer-trust-reduced-to-isolation.md) — the same
  supersession convention applied to the trust model
- `internal/plugins/torque/`, `internal/plugins/tesseract/` — the two plugins
  whose recorded "exception" this document corrects to description
- `internal/pluginhost/tools.go`, `internal/pluginhost/child.go` — `ToolCaller`
  and the subprocess wire, the source for §5's mode distinction
- `CW-20260911-0057`, `CW-20260910-0034`, `CW-20260911-0070` — the tasks this
  document closes and builds on
