# ADR 0009: The Renderer Trust Model, Reduced to Isolation

**Status:** Accepted.

**Approved:** 2026-09-11 by Chrispian, through two surfaces and worth
distinguishing. The decision itself, and the four rulings this document raised in
draft, were made in a **Tangent approval queue** — room
`e627c5db-cf08-4df6-80c0-0996c0fda62b`, orchestrated from session
`session-20260911-bd8cea23`. The **document** was signed off in chat:
*"0009 looks good. Approved."* He asked for the review that produced it —
*"let's really look at it now and make a call"* — and the call was absorb now,
not defer.

**Drafted `Proposed`, promoted on approval**, per the convention ADR 0008
established: an agent cannot write an approval line for a document Chrispian has
not read, so it lands `Proposed` and only he moves it. Promotion is a status
change and an approval line, nothing else — every decision date inside this
document is unchanged by it.

**Date:** 2026-09-11

**Task:** `CW-20260911-0060`

**Baseline reviewed:** `f45d3f6`. Every inventory and file reference below was
read at that commit.

**Supersedes, in part:** ADR 0003 §2.3 and §2.7, ADR 0007 §4, and ADR 0008's
open questions 1 and 2, in exactly the ways *"What this supersedes"* enumerates.
**Those documents are not edited.** An accepted ADR is a log of a decision that
was made, and a reversal is a new record that supersedes while the old one
stands as the log — Chrispian, 2026-09-11: *"They are logs of decisions that
WERE made. They are not a list of rules to be followed. It's history, not
projection."* Amending ADR 0007 §4 in place, twice in two days, is the drift
`CW-20260911-0040` spent a day repairing; this document is the shape that
repair implies.

## Context

Tangent's renderer trust model does two different jobs under one name.

**Isolation** decides where a renderer's code runs and what the browser will
let it reach. **Provenance** decides who is allowed to supply a renderer in the
first place — which publisher, verified how, shipping its bundle where.

The first is enforced by the browser. The second is enforced by a table, and it
was built for a distribution that does not exist.

### The inventory that drove the decision

Read from the manifests at `f45d3f6`, not assumed. This is a dated observation
about one distribution at one commit, which is what an ADR is for:

| Requested class | Renderer shape | Count | What they are |
|---|---|---|---|
| `core-trusted` | `react-component` | 17 | Tangent's own components, compiled into `ui_dist` |
| `portfolio-trusted` | `react-component` | 1 | `tangent.whiteboard` — the same, plus an import of tldraw |
| `sandboxed-code` | `sandboxed-frame` | 1 | `tangent.design-iteration` |

`declarative` and `external-surface` have no instances at all.

So the provenance apparatus sorts nineteen renderers written by one person,
reviewed in one release, into three buckets — and the two largest buckets differ
by exactly one capability.

### Three facts the tree already records

Each of these was written down before this decision, by the people building the
thing, and each one is the apparatus reporting on itself:

1. **The two main-origin classes are enforced identically.**
   `internal/definition/trust.go:196-202`, in its own words: *"There is no
   signature verifier in v0.x. `signed-package` is not [Assurance.Grantable], so
   an out-of-tree portfolio package is refused at the assurance gate rather than
   admitted unverified. The consequence is honest and worth stating: for the
   material that ships in this tree, portfolio-trusted is today enforced exactly
   as strictly as core-trusted and no more."*

2. **The one capability separating them is unreachable and unimplemented.**
   `core-trusted`'s ceiling is `portfolio-trusted`'s plus `process.exec`
   (`trust.go:180-212`). And `process.exec` is
   `{authz.Administer, MediationUnimplemented}`
   (`internal/effect/capability.go:181`), whose own comment reads: *"Nothing
   holds `administer` in the shipped binary (ADR 0004 §7), and there is no
   executor."* The sole distinction between the two classes is a grant no
   principal can hold for an effect no code performs.

3. **What blocked `portfolio-trusted` was an unbuilt feature, not a policy.**
   `signed-package` is expressible and has no verifier
   (`internal/definition/manifest.go:196-201`). A publisher outside the tree is
   refused at the assurance gate because the check does not exist — not because
   anyone decided such a publisher must be signed. **An unbuilt feature reading
   as a security boundary is the specific failure this ADR exists to correct**,
   and it is the same failure ADR 0008 §2 found in §4's runtime-asset-loading
   exclusion: an absence that later arguments were built on top of as though it
   were structural.

### Two decisions that removed the apparatus's remaining subject

- **Plugin-contributed kinds are first-party** (ADR 0008 §4). Third-party kind
  contribution is out of scope and nothing prepares for it.
- **Signing is dead portfolio-wide.** Chrispian, 2026-09-11: *"most other
  systems do not require plugins to be signed. Use at your own risk... I don't
  think we want to be the gate on what's allowed... That would actually make us
  liable which is worse to me."* A plugin directory may still exist; a
  signing or approval gate does not.

With no third-party publisher and no signature to check, the provenance half
polices an adversary that does not exist. Chrispian's argument, which is the
one that settles it: *"An agent is already running on the system and could just
execute code... every single agent setup has tool permissions and if they have
bash, they already have everything on the system. What problem are we actually
solving?"*

### Why now, and not later

Not hypothetical: **the trust model is actively distorting the loader
extraction.** `CW-20260911-0035`'s phase-1 proposal is built around a seam
between resolution and execution, and that seam exists for one reason — at
`f45d3f6` no trust class admits a separately served bundle, so Tangent cannot
run a plugin's renderer at all. Building the loader first means building a seam
to satisfy a rule that is being deleted, and then living with the seam.

## Decision

### 1. The trust class collapses into the isolation it produced

ADR 0003 §2.3 says a granted class produces exactly two facts — an isolation and
a capability ceiling — and that *"a class that produces neither is decoration."*
Applying that test at `f45d3f6`: `core-trusted` and `portfolio-trusted` produce
the same isolation and ceilings that differ by an unreachable capability. By §2.3's
own standard the distinction between them is decoration.

So the five requested classes reduce to the isolations that were always the
enforceable part:

| Isolation | Publisher code runs? | Enforced by |
|---|---|---|
| `main-origin` | yes, with the document's authority | admission — the kind is classified, or it is not drawn |
| `host-primitive` | no | nothing publisher-authored executes |
| `sandboxed-frame` | yes, untrusted | `sandbox="allow-scripts"`, opaque origin, frame CSP |
| `external-surface` | not in Tangent | n/a |

**`core-trusted` and `portfolio-trusted` become one main-origin class.** The
whiteboard importing tldraw is a dependency choice inside a component Tangent
ships, reviewed in the same release by the same person as the other seventeen.
That is a code-review fact, not a runtime boundary.

**The field is renamed `renderer.trust_class` → `renderer.isolation`.** These
manifests are taking a version bump regardless (see Consequences), so the rename
costs nothing where renaming a manifest field is otherwise expensive, and a field
called `trust_class` carrying a value that is not a trust class is the same kind
of misnomer this ADR exists to remove.

#### The name does not lie, and that is checkable rather than asserted

The field stays publisher-declared, and ADR 0003 §2.3's *"a manifest requests;
Tangent decides"* survives unchanged (review question 2). That raises a fair
objection to the new name: a field called `isolation` reads as a statement of
fact, and if it named only what a publisher *asked for*, the rename would
reproduce the defect it is fixing in a new spelling.

It does not, and the reason is a property of the code rather than an intention.
**Materialization never downgrades.** `internal/definition/materialize.go:237`
assigns the profile looked up from the *declared* value; every path that
disagrees with the declaration quarantines the definition instead. So there is
no state in which a materialized definition's effective isolation differs from
its declared one — the declaration is either exactly right or the definition did
not materialize.

The decide step is therefore real but is a **refusal, never a substitution**.
What it still refuses, with the provenance apparatus gone: a manifest whose
`renderer.class` and `renderer.isolation` disagree, in both directions — a
`react-component` claiming `sandboxed-frame`, a `sandboxed-frame` claiming
`main-origin`. That check is what makes the field's name true, so it is load
bearing in a way it was not before, and it must not be relaxed on the grounds
that the trust model got simpler.

### 2. The sandbox stays, and Chrispian's argument does not reach it

`tangent.design-iteration` renders HTML and CSS **an agent wrote**, in the
participant's browser, at Tangent's origin, in a session holding their cookie
and `/api/effects` access.

**The agent is not the adversary. It is the conduit.** An agent summarizes a web
page, reads a file, pipes a model's output — and that content becomes executable
markup running with the participant's authority in the browser. *"The agent
could already run code on the box"* is true and does not reach this path: the
box is not the thing being protected here, the participant's browser session is,
and the content did not originate with the agent.

This case grows rather than shrinks. Any *"render this thing I found"* surface
has the same shape, and there will be more of them.

So the sandbox's three layers stay exactly as they are: `allow-scripts` and
nothing else, a `default-src 'none'` frame CSP whose `script-src` is a hash of
Tangent's own shim, and `Permissions-Policy: clipboard-write=(self)` on the
document. So does the four-check `postMessage` rule in
`ui/src/lib/sandbox-frame.ts`. **Nothing in this ADR relaxes any of them**, and
a future simplification that reaches them is a different decision needing its
own record.

### 3. Renderer class stays, as a shape declaration

`renderer.class` — `react-component` / `declarative` / `sandboxed-frame` /
`external-surface` — answers *"how is this drawn"*, which is useful with or
without a trust question. It stays, and so does the coherence check that refuses
a manifest whose shape and isolation disagree in either direction. A
`react-component` that claims `sandboxed-frame` is still a defect worth catching
at parse time.

### 4. Capabilities: keep what a browser primitive enforces

The capability namespace splits by whether anything actually stops you:

**Kept, because they are real enforcement:**
- `network.fetch` — `connect-src 'self'` on the document means no renderer in any
  isolation can reach an external origin itself. The host is the only possible
  actor and, with no performer registered, the effect is refused rather than
  admitted (`internal/effect/capability.go:163-168`).
- The sandbox CSP wholesale, per §2.

**Removed: the class-based ceiling.** `required_capabilities` stays in the
manifest and keeps saying what a renderer needs; what goes is the
materialization-time check that refuses a capability because the renderer's
class does not permit it (`trustCeilingDenials`).

This is deliberately neither of the two options the draft offered, and the third
is better than both. *Deleting the declaration* would lose a useful statement of
what a renderer needs. *Demoting the ceiling to documentation* would keep a table
that reads like a gate and is not one — which is precisely the defect this ADR is
about, rebuilt one layer down. **Keep the information, drop the pretense.**

What still refuses a capability is unchanged and is real: host policy's
`GrantableCapabilities` intersection, and the mediation table — a capability with
no registered performer is refused with `effect_unavailable` rather than admitted
(`internal/effect/capability.go`).

`process.exec` sits outside both halves: it is unreachable and unimplemented
(Context, fact 2). It stays reserved in the namespace and grants nothing, which
is what it does today.

### 5. The provenance apparatus is removed

Specifically, these four, and nothing else:

1. **The class-conditional trust-evidence gate** — `trustEvidenceSupports`,
   `trust.go:320-342`, in full. Note the precision: the **standalone** assurance
   gate at `materialize.go:216`, which quarantines any definition whose
   `trust.assurance` has no verifier, **stays**. `trustEvidenceSupports`
   re-checked it as a defensive precondition — its own comment says so — and
   only the class-conditional half is provenance apparatus. Keeping the
   standalone gate is what keeps ADR 0003 §2.7's *"`unverified` must not become
   a permissive fallback"* true rather than merely restated.
2. **Publisher reservation** — `trust.go:329-334`, restricting a class to
   publisher `tangent` or `hollis-labs/go-envelopes`.
3. **The empty-`asset_digest` rule** — `trust.go:335-340`, on the stated grounds
   that *"a separately distributed bundle is by definition not the one that went
   through the release review."*
4. **The `core-trusted` / `portfolio-trusted` distinction**, per §1.

`trust.source_locator`, `trust.verified_at` and `trust.quarantine_reason` are
**not** removed. They are provenance *records*, and a record of where a manifest
came from is useful exactly when something has gone wrong. What goes is the
apparatus that refused on them.

## What this supersedes

Named precisely, so a reader of the superseded text knows what still holds. None
of these documents is edited.

**ADR 0003 §2.3**, the `renderer.trust_class` row: the field name, the
five-value enum, and the ceiling ordering
`core ⊃ portfolio ⊃ sandboxed ⊃ declarative = external = ∅` — which is not
reduced but **removed**, per §4.
What survives: *"A manifest requests a class; Tangent policy decides"*, the
two-facts test, and *"isolation is never a manifest field"* — see review
question 2, which is the one open item this ADR leaves.

**ADR 0003 §2.7**, the trust-evidence table, in the narrow sense that
`trust.assurance` stops deciding which *class* may be granted. Everything else
in §2.7 stands, and one clause stands **because it is deliberately not touched**:
*"`unverified` must not become a permissive fallback"* is still enforced, by the
standalone gate at `materialize.go:216` that §5 keeps. A definition whose
assurance has no verifier is still quarantined; what changed is that this is no
longer also a statement about which renderer class it may reach.

**ADR 0003's D1 amendment (2026-09-04)**, which introduced the ceiling ordering
this reduces.

**ADR 0007 §4**, one clause of one bullet: *"A plugin cannot author its own
trust class, granted capabilities, effective assurance, or asset digest."* The
**rule survives and matters** — a plugin still does not author its own
containment. What changes is that the set of things it could have authored is
now smaller. Every other sentence in §4 stands, including the boundary rule that
`CW-20260911-0040` narrowed §4 down to.

**Not superseded, and worth saying because it sits next door:** ADR 0003 §3's
identity rules. This change moves `contract_digest` on nineteen manifests and is
therefore a **version** bump on each, exactly as §3 requires. §3 is being obeyed
here, not revised.

## ADR 0008's open questions 1 and 2 dissolve

They are not answered. They were artifacts of the apparatus being removed, and
removing it leaves them without a subject. The distinction matters: an answered
question produces a rule, and a dissolved one produces nothing.

**Open question 1 — how `core-trusted` survives runtime bundle loading —
dissolves completely.** It named three obstacles: the publisher reservation, the
grantable-assurance requirement, and the empty-`asset_digest` rule. §5 removes
all three. There is no longer a `core-trusted` for a served bundle to fail to
reach, and no admission rule for it to fail.

**Open question 2 — CSP and the renderer sandbox — largely dissolves, and the
honest version is that it splits in two.**

- For a **main-origin** renderer the obstacle is gone, and it turns out it was
  never in the CSP. Tangent's document policy is
  `script-src 'self' '<shim hash>'` (`internal/server/csp.go:133`), so a bundle
  served from Tangent's own origin is already `'self'` and already admitted. What
  blocked a served bundle was the trust class, not the browser.
- For a **sandboxed-frame** renderer the frame CSP still refuses an external
  module, and that is the sandbox doing its job rather than an obstacle. A
  `sandboxed-frame` renderer's entry is a frame document, not an ES module the
  page imports, so the question does not arise for it.

What genuinely remains, and it is smaller than the question as posed: a
**cross-origin** bundle would need `script-src` to name its origin. Nothing
currently proposes one. Recorded so its absence is not read as an answer.

## Consequences

### Positive

- **The model describes what the browser enforces, and nothing else.** Every
  remaining distinction has a mechanism behind it.
- **`CW-20260911-0035` can resume against a model that admits a served bundle.**
  Its phase-1 resolution/execution seam was designed around a constraint that
  this ADR deletes; the loader extraction gets to be about loading.
- **An unbuilt feature stops reading as a security boundary.** That specific
  confusion has now caused two expensive errors in this repository — the
  runtime-asset-loading exclusion ADR 0008 §2 reversed, and the
  `signed-package`-shaped gate this ADR removes.
- **The thing worth protecting gets clearer by having less around it.** After
  this, one renderer is sandboxed and the reason is stated in one paragraph.

### Negative and costs

- **A version bump on nineteen manifests, with the ADR 0003 §8 C1 blast
  radius.** `trust_class` is in the contract, so changing it moves
  `contract_digest`, which §3 makes a **version** bump and not a revision —
  a revision is a non-semantic edit counter and this is semantic. Under §8 C1 a
  registry change that alters the current binding renders the pinned definition
  unavailable for new submissions, so in-flight interactions of every kind are
  affected on upgrade. **Paid once, knowingly, rather than nineteen times as
  kinds change individually.** `contractLock` moves with it, and it must not be
  edited to silence a §3 finding — it is the gate.
- **Capability ceilings become documentation, and documentation is weaker.** If a
  third-party publisher ever arrives, the ceiling has to be re-enforced rather
  than merely re-read. That is a real cost of sizing the model for the
  distribution that exists.
- **A reader of ADR 0003 §2.3 now needs this document to know what still
  holds.** That is the cost of superseding rather than editing, and it is the
  cost this repository has decided to pay.

### Risks

- **The simplification takes the sandbox with it.** The most likely failure mode
  is a future reader who reads *"the agent could already run code on the box"* as
  general and applies it to `design-iteration`. §2 exists to make that argument
  fail on contact, and the conduit distinction is why.
- **A third-party publisher arrives and nobody notices the model was sized for
  first-party.** Mitigated by ADR 0008 §4 scoping kinds to first-party
  explicitly, so admitting a third party is already a decision requiring its own
  record.
- **`process.exec` acquires an executor without the ceiling being reconsidered.**
  It is reserved and grants nothing today; if something ever performs it, the
  question of who may declare it returns and this ADR does not answer it.

## Alternatives considered

### Amend ADR 0003 §2.3 and ADR 0007 §4 in place

Rejected by Chrispian, 2026-09-11, and this is the alternative whose rejection
matters most. An accepted ADR is a log of what was decided when; editing it
destroys the record and makes a later reader unable to tell what was true at the
time. Amending §4 in place is the mechanical cause of the drift
`CW-20260911-0040` repaired, and doing it again — in the same week, to the same
section — would be learning nothing.

### Keep the apparatus and defer, since nothing is broken today

Rejected. It is not inert: it is shaping the loader extraction's design right
now (Context, *"Why now"*). A model nobody can satisfy is not neutral while it
is unsatisfied.

### Keep `portfolio-trusted` for the whiteboard, because it imports third-party code

Rejected, and it is the most tempting of these. tldraw is a real third-party
dependency. But it is a dependency chosen, reviewed and bundled by the same
people in the same release as the rest of `ui_dist`, and the class does not
isolate it — both classes are `main-origin`. A supply-chain concern about a
bundled npm dependency is real and is a *dependency review* question, which a
renderer trust class does not answer and never did.

### Remove the sandbox too, on the same argument

Rejected. See §2. The argument that retires the provenance apparatus is that
there is no adversary; the sandbox's adversary is content flowing *through* an
agent from somewhere else, and that one exists.

### Reduce to isolation but keep the five class names as aliases

Rejected. Names that no longer produce a distinction are exactly the decoration
ADR 0003 §2.3's own test rejects, and keeping them would preserve the
vocabulary that made an unbuilt verifier read as a policy.

## Review questions

All four were raised in draft and ruled by Chrispian on 2026-09-11, the same day.
Recorded rather than removed, because what was asked is part of the log.

1. **Is `renderer.trust_class` the right field name for a value that now names an
   isolation?** — **Ruled: rename to `renderer.isolation`.** The version bump is
   paid regardless, so the rename costs nothing where renaming a manifest field
   is otherwise expensive. §1 carries it, along with the check that the new name
   is true rather than merely shorter.
2. **Does ADR 0003 §2.3's *"a manifest requests; Tangent decides"* survive?** —
   **Ruled: yes, unchanged.** §2.3's model stands; only the evidence it weighed
   is gone. The decide step is now a refusal and never a substitution, which §1
   states plainly rather than leaving to be inferred from a field name.
3. **Delete the capability ceilings, or demote them to documentation?** —
   **Ruled: neither. Keep the declaration, remove the class-based ceiling.**
   §4 carries it. Information without pretense.
4. **Is the §8 C1 blast radius acceptable on this timing?** — **Ruled: pay it
   now**, and fold `CW-20260911-0045` into the same bump: two comments in the
   `tangent.app-board` manifest have been knowingly false since `adca3ff`,
   deferred precisely because correcting them would have moved `binding_digest`
   for nothing else. This is the byte-moving change they were waiting for.

## References

- [ADR 0003](0003-definition-and-package-ownership.md) §2.3, §2.7, §3, §8 — the manifest, the trust fields, the identity rules, and C1
- [ADR 0004](0004-caller-participant-and-room-access-authority.md) §7 — the capability rows; why nothing holds `administer`
- [ADR 0007](0007-collaboration-surface-plugin-host-and-view-state.md) §4 — the plugin host boundary, which survives this
- [ADR 0008](0008-the-plugin-model.md) §4 and its open questions 1 and 2 — first-party scoping, and the two questions that dissolve
- `docs/renderer-trust-classes.md` — the shipped model this reduces; the sandbox half of it stands
- `internal/definition/trust.go`, `internal/definition/manifest.go`, `internal/effect/capability.go`, `internal/server/csp.go`, `ui/src/lib/sandbox-frame.ts` — the source read for every claim above
